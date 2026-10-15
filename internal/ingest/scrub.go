package ingest

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// jidPattern matches user[.agent][:device]@server for the servers that carry a phone number, LID or group ID.
// An old-style group user is <creator>-<timestamp>.
var jidPattern = regexp.MustCompile(`([0-9]+(?:-[0-9]+)?)((?:\.[0-9]+)?(?::[0-9]+)?)@(s\.whatsapp\.net|lid|g\.us|c\.us|hosted\.lid|hosted|broadcast)`)

// barePhone is a whole string that is a phone number; it is replaced only in identifier-named fields.
var barePhone = regexp.MustCompile(`^\+?[0-9]{8,}$`)

// Numbers inside text: a mention token (@<number or LID>), a vCard waid and a vCard TEL value.
var (
	mentionPattern = regexp.MustCompile(`@([0-9]{8,})`)
	waidPattern    = regexp.MustCompile(`waid=([0-9]+)`)
	telPattern     = regexp.MustCompile(`(?im)^((?:[a-z0-9-]+\.)?TEL[^:\r\n]*:)([^\r\n]*)`)
	nonDigits      = regexp.MustCompile(`[^0-9]`)
)

// scrubber holds the secret key of the fakes.
type scrubber struct{ key []byte }

// Scrub copies the recorded files in `in` to `out`, replacing every phone number, LID and group ID in the JSON and
// in every protobuf string field with a fake keyed by key (at least 32 bytes), and clearing key material and
// payloads. Text is kept. The same key gives the same fakes in every run; without the key they cannot be reversed.
func Scrub(in, out string, key []byte) error {
	if len(key) < scrubKeySize {
		return fmt.Errorf("scrub key must be at least %d bytes", scrubKeySize)
	}
	sc := scrubber{key: key}
	return filepath.WalkDir(in, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(in, path)
		if err != nil {
			return err
		}
		target := filepath.Join(out, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if data, err = sc.scrubFile(data); err != nil {
			return fmt.Errorf("scrub %s: %w", rel, err)
		}
		return os.WriteFile(target, data, 0o600)
	})
}

// scrubFile scrubs one JSON file; a recorded file's base64 protobuf is decoded, walked and re-encoded.
func (sc scrubber) scrubFile(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	doc = sc.scrubJSON(doc, "")
	if top, ok := doc.(map[string]any); ok {
		if encoded, ok := top["proto"].(string); ok {
			kind, _ := top["kind"].(string)
			scrubbed, err := sc.scrubPayload(kind, encoded)
			if err != nil {
				return nil, err
			}
			top["proto"] = scrubbed
		}
	}
	return json.MarshalIndent(doc, "", "  ")
}

func (sc scrubber) scrubJSON(v any, field string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch _, isString := val.(string); {
			case k == "Certificate": // a verified-name certificate: signed bytes with the business details
				x[k] = nil
			case k == "Content" && isString: // the base64 content of an unknown group change node
				x[k] = ""
			default:
				x[k] = sc.scrubJSON(val, k)
			}
		}
	case []any:
		for i, val := range x {
			x[i] = sc.scrubJSON(val, field)
		}
	case string:
		return sc.scrubString(x, field)
	}
	return v
}

func (sc scrubber) scrubPayload(kind, encoded string) (string, error) {
	newMsg, ok := protoKinds[kind]
	if !ok {
		return "", fmt.Errorf("cannot scrub the protobuf of kind %q", kind)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	msg := newMsg()
	if err = (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(raw, msg); err != nil {
		return "", err
	}
	sc.scrubMessage(msg.ProtoReflect())
	raw, err = proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(msg)
	return base64.StdEncoding.EncodeToString(raw), err
}

// scrubMessage walks every populated field, recursing into nested, repeated and map values. Unknown fields are
// dropped because they cannot be inspected.
func (sc scrubber) scrubMessage(m protoreflect.Message) {
	m.SetUnknown(nil)
	var fields []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fields = append(fields, fd)
		return true
	})
	for _, fd := range fields {
		name := string(fd.Name())
		switch v := m.Get(fd); {
		case fd.Kind() == protoreflect.BytesKind && clearedBytesField(name):
			m.Clear(fd)
		case fd.IsMap():
			sc.scrubMap(v.Map(), fd, name)
		case fd.IsList():
			for i, l := 0, v.List(); i < l.Len(); i++ {
				if nv, ok := sc.scrubValue(fd.Kind(), l.Get(i), name); ok {
					l.Set(i, nv)
				}
			}
		default:
			if nv, ok := sc.scrubValue(fd.Kind(), v, name); ok {
				m.Set(fd, nv)
			}
		}
	}
}

func (sc scrubber) scrubMap(mp protoreflect.Map, fd protoreflect.FieldDescriptor, field string) {
	var keys []protoreflect.MapKey
	mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	for _, k := range keys {
		v := mp.Get(k)
		if nv, ok := sc.scrubValue(fd.MapValue().Kind(), v, field); ok {
			v = nv
		}
		if fd.MapKey().Kind() == protoreflect.StringKind {
			mp.Clear(k)
			k = protoreflect.ValueOfString(sc.scrubString(k.String(), field)).MapKey()
		}
		mp.Set(k, v)
	}
}

// scrubValue scrubs a nested message in place, or returns the replacement of a string.
func (sc scrubber) scrubValue(kind protoreflect.Kind, v protoreflect.Value, field string) (protoreflect.Value, bool) {
	switch kind {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		sc.scrubMessage(v.Message())
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(sc.scrubString(v.String(), field)), true
	}
	return v, false
}

// clearedBytesField names the bytes fields cleared by Scrub: keys, key data, secrets, tokens and sender keys, and
// serialized or compressed payloads (inline history, resent messages, LID migration mappings) that cannot be walked.
// Fixtures never need them.
func clearedBytesField(name string) bool {
	n := strings.ToLower(name)
	for _, suffix := range []string{"key", "keydata", "secret", "token", "bytes"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return strings.Contains(n, "senderkeydistribution") || strings.Contains(n, "payload")
}

// scrubString replaces every JID's user part, keeping agent, device and server, and every number in a vCard TEL
// value, a waid or a mention token with the same fake, so a mention in the text still matches its mentionedJID.
// A whole-string phone number is replaced too when the field name says it holds an identifier.
func (sc scrubber) scrubString(s, field string) string {
	s = jidPattern.ReplaceAllStringFunc(s, func(match string) string {
		p := jidPattern.FindStringSubmatch(match)
		return sc.fakeUser(p[1]) + p[2] + "@" + p[3]
	})
	s = telPattern.ReplaceAllStringFunc(s, func(match string) string {
		p := telPattern.FindStringSubmatch(match)
		digits := nonDigits.ReplaceAllString(p[2], "")
		if digits == "" {
			return match
		}
		plus := ""
		if strings.HasPrefix(strings.TrimSpace(p[2]), "+") {
			plus = "+"
		}
		return p[1] + plus + sc.fakeUser(digits)
	})
	s = waidPattern.ReplaceAllStringFunc(s, func(match string) string { return "waid=" + sc.fakeUser(match[len("waid="):]) })
	s = mentionPattern.ReplaceAllStringFunc(s, func(match string) string { return "@" + sc.fakeUser(match[1:]) })
	if identifierField(field) && barePhone.MatchString(s) {
		plus := ""
		if strings.HasPrefix(s, "+") {
			plus = "+"
		}
		return plus + sc.fakeUser(strings.TrimPrefix(s, "+"))
	}
	return s
}

func identifierField(name string) bool {
	n := strings.ToLower(name)
	for _, word := range []string{"jid", "participant", "phone", "number"} {
		if strings.Contains(n, word) {
			return true
		}
	}
	return false
}

// fakeUser is a deterministic stand-in for a JID user part: HMAC-SHA256 of it under the scrub key, so the same
// input maps to the same fake in every run with that key. The leading 0 marks it as fake (E.164 numbers never
// start with 0).
func (sc scrubber) fakeUser(user string) string {
	mac := hmac.New(sha256.New, sc.key)
	_, _ = mac.Write([]byte(user))
	return fmt.Sprintf("0%014d", binary.BigEndian.Uint64(mac.Sum(nil)[:8])%100_000_000_000_000)
}

const scrubKeySize = 32

// LoadScrubKey reads the scrub key at path, creating it with 32 random bytes and mode 0600 the first time.
// The key must stay outside the repository: whoever has it can test guesses against the fakes.
func LoadScrubKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, scrubKeySize)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		var f *os.File
		if f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err != nil {
			return nil, err
		}
		_, err = f.Write(key)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("scrub key %s: %w", path, err)
	}
	if len(key) < scrubKeySize {
		return nil, fmt.Errorf("scrub key %s is shorter than %d bytes", path, scrubKeySize)
	}
	return key, nil
}
