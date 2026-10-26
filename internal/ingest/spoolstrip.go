package ingest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"whatsapp-archiver/internal/store"
)

// This file is the I3 decision (Hamza, option b, 26 Oct 2026): history_spool keeps the decrypted HistorySync blob for
// seven days after import, and archive.db is backed up every night, so the blob is stripped of key material before it
// is spooled. The history handler (W5) must call StripHistorySecrets (or StripHistorySync, when it holds the parsed
// chunk) before store.SpoolReceive. The rule is the one the live path follows for raw_proto: message secrets and
// keys never reach the archive. A message's own media key stays, because the media worker needs it and
// messages.raw_proto already stores it.

// maxBlobBytes bounds the decompressed size of a spooled blob (a decompression bomb must not exhaust memory). Real
// chunks are some tens of MB.
const maxBlobBytes = 1 << 30

// secretFieldWords are lower-case fragments of bytes-field names whose value is key material or a secret token. A
// bytes field in any message at any depth (nested, quoted, edited, wrapped, a reaction or a poll update, a status) is
// cleared when its name contains one. Examples: messageSecret, botMessageSecret, threadIDUserSecret, rootSecret,
// tcToken, acAuthTokens, nctSalt, shareableChatIdentifierEncryptionKey, contactPrimaryIdentityKey, encKey (a poll's),
// revealKey, callKey, keyData, groupRootKey, reportingTag, axolotlSenderKeyDistributionMessage. mediaKey,
// fileEncSHA256 and fileSHA256 are not in the list.
var secretFieldWords = []string{
	"secret", "token", "salt", "enckey", "encryptionkey", "revealkey", "callkey", "keydata", "masterkey", "grouprootkey",
	"frankingkey", "privatekey", "identitykey", "reportingtag", "senderkeydistributionmessage",
}

// secretFields are bytes fields cleared by their full name, because their name alone would also catch message media.
var secretFields = map[protoreflect.FullName]bool{
	// A saved sticker (not a message): its media key is never needed to import history.
	"WAWebProtobufsHistorySync.StickerMetadata.mediaKey": true,
}

// StripHistorySecrets takes a decrypted HistorySync blob in its spool format (store.BlobZlib or store.BlobRaw), clears
// the key material of every conversation and message in it (see secretFieldWords, secretFields and the protocol-message
// payloads of dropKeyPayloads) and returns the blob in the same format. Everything else is kept, field for field, so
// the importer sees the same chunk. It is idempotent: stripping a stripped blob returns the same bytes. A blob that
// cannot be read (unknown format, bad zlib, bad protobuf, trailing data, over maxBlobBytes) is an error, and the caller
// must not spool it. The input is not modified.
func StripHistorySecrets(blob []byte, format string) ([]byte, error) {
	var raw []byte
	switch format {
	case store.BlobRaw:
		raw = blob
	case store.BlobZlib:
		var err error
		if raw, err = inflate(blob); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("strip history secrets: unknown blob format %q", format)
	}
	hs := &waHistorySync.HistorySync{}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(raw, hs); err != nil {
		return nil, fmt.Errorf("strip history secrets: decode history sync: %w", err)
	}
	stripMessage(hs.ProtoReflect())
	clean, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(hs)
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: encode history sync: %w", err)
	}
	if format == store.BlobRaw {
		return clean, nil
	}
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err = zw.Write(clean); err != nil {
		return nil, fmt.Errorf("strip history secrets: compress: %w", err)
	}
	if err = zw.Close(); err != nil {
		return nil, fmt.Errorf("strip history secrets: compress: %w", err)
	}
	return out.Bytes(), nil
}

// StripHistorySync returns a copy of hs without key material, by the same rule as StripHistorySecrets. hs itself is not
// modified. A nil hs gives nil.
func StripHistorySync(hs *waHistorySync.HistorySync) *waHistorySync.HistorySync {
	if hs == nil {
		return nil
	}
	clean := proto.Clone(hs).(*waHistorySync.HistorySync)
	stripMessage(clean.ProtoReflect())
	return clean
}

func inflate(blob []byte) ([]byte, error) {
	src := bytes.NewReader(blob)
	zr, err := zlib.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: open zlib blob: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxBlobBytes+1))
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: read zlib blob: %w", err)
	}
	if len(raw) > maxBlobBytes {
		return nil, fmt.Errorf("strip history secrets: blob is over %d bytes decompressed", maxBlobBytes)
	}
	if src.Len() > 0 {
		return nil, fmt.Errorf("strip history secrets: %d bytes follow the zlib stream", src.Len())
	}
	return raw, nil
}

// stripMessage clears the key material of m and of every message nested in it, in place.
func stripMessage(m protoreflect.Message) {
	if pm, ok := m.Interface().(*waE2E.ProtocolMessage); ok {
		dropKeyPayloads(pm)
	}
	var fields []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fields = append(fields, fd)
		return true
	})
	for _, fd := range fields {
		switch {
		case fd.Kind() == protoreflect.BytesKind:
			if isSecretField(fd) {
				m.Clear(fd)
			}
		case fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind:
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				m.Get(fd).Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					stripMessage(v.Message())
					return true
				})
			}
		case fd.IsList():
			list := m.Get(fd).List()
			for i := 0; i < list.Len(); i++ {
				stripMessage(list.Get(i).Message())
			}
		default:
			stripMessage(m.Get(fd).Message())
		}
	}
}

func isSecretField(fd protoreflect.FieldDescriptor) bool {
	if secretFields[fd.FullName()] {
		return true
	}
	name := strings.ToLower(string(fd.Name()))
	for _, word := range secretFieldWords {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// dropKeyPayloads removes the payloads of the protocol messages that carry keys (the keyMaterial types of normalize.go):
// a history notification (its media key), an app-state key share, a peer-data response and a contact hash key share.
// The message and its type stay, as the normaliser stores them without raw_proto.
func dropKeyPayloads(pm *waE2E.ProtocolMessage) {
	pm.HistorySyncNotification = nil
	pm.AppStateSyncKeyShare = nil
	pm.PeerDataOperationRequestResponseMessage = nil
	pm.SharedDeviceContactHashKeyShare = nil
}
