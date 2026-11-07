package archiveread

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type orderKey struct {
	TS     int64  `json:"ts"`
	Stored string `json:"chat"`
	ID     string `json:"id"`
}
type pageCursor struct {
	Version int      `json:"v"`
	Hash    string   `json:"f"`
	Mapping string   `json:"p"`
	Archive string   `json:"a"`
	Expires int64    `json:"e"`
	Last    orderKey `json:"k"`
	From    int64    `json:"from"`
	To      int64    `json:"to"`
}

func filterHash(tool string, a arguments) string {
	a.Cursor = ""
	a.Limit = nil
	b, _ := json.Marshal(struct {
		Tool string
		Args arguments
	}{tool, a})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (e *Engine) encodeCursor(s *snapshot, hash string, last orderKey, from, to int64) string {
	c := pageCursor{Version: 1, Hash: hash, Mapping: s.ids.version, Archive: s.fingerprint, Expires: time.Now().Add(15 * time.Minute).Unix(), Last: last, From: from, To: to}
	b, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, e.secret[:])
	_, _ = mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (e *Engine) decodeCursor(raw string, s *snapshot, hash string) (pageCursor, error) {
	var c pageCursor
	if raw == "" {
		return c, nil
	}
	if len(raw) > 4096 {
		return c, failure("INVALID_CURSOR")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return c, failure("INVALID_CURSOR")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, failure("INVALID_CURSOR")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, failure("INVALID_CURSOR")
	}
	mac := hmac.New(sha256.New, e.secret[:])
	_, _ = mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Hash != hash || c.Mapping != s.ids.version || c.Archive != s.fingerprint || c.Expires <= time.Now().Unix() {
		return pageCursor{}, failure("INVALID_CURSOR")
	}
	return c, nil
}
func messageRef(chat, id string) string {
	b, _ := json.Marshal([]string{"1", chat, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func parseRef(ref string) (string, string, error) {
	if len(ref) > 4096 {
		return "", "", failure("NOT_FOUND_OR_NOT_ALLOWED")
	}
	b, e := base64.RawURLEncoding.DecodeString(ref)
	var p []string
	if e != nil || json.Unmarshal(b, &p) != nil || len(p) != 3 || p[0] != "1" || p[1] == "" || p[2] == "" || len(p[1]) > 512 || len(p[2]) > 1024 {
		return "", "", failure("NOT_FOUND_OR_NOT_ALLOWED")
	}
	return p[1], p[2], nil
}
