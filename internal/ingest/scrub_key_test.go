package ingest_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/ingest"
)

// testKey makes every Scrub in the tests deterministic.
var testKey = bytes.Repeat([]byte{7}, 32)

// unkeyedFake is the fake of the first H14 version: a bare SHA-256 of the number, reversible by enumeration.
func unkeyedFake(user string) string {
	sum := sha256.Sum256([]byte(user))
	return fmt.Sprintf("0%014d", binary.BigEndian.Uint64(sum[:8])%100_000_000_000_000)
}

func scrubbedSenderWith(t *testing.T, in string, key []byte) string {
	t.Helper()
	out := t.TempDir()
	if err := ingest.Scrub(in, out, key); err != nil {
		t.Fatal(err)
	}
	evts, err := ingest.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	return evts[0].(*events.Message).Info.Sender.User
}

// Fix round 1, I3: the fake is keyed: the same key gives the same fake, another key another one, and the old
// unkeyed hash of the number does not reproduce it. A missing or short key is refused.
func TestScrubFakeIsKeyed(t *testing.T) {
	in := t.TempDir()
	e := messageEvent("1@g.us", "A", text("x"))
	e.Info.Sender = jid(phoneA + ":12@s.whatsapp.net")
	ingest.NewRecorder(in).Wrap(func(any) {})(e)
	first, again := scrubbedSenderWith(t, in, testKey), scrubbedSenderWith(t, in, testKey)
	other := scrubbedSenderWith(t, in, bytes.Repeat([]byte{8}, 32))
	if first != again {
		t.Errorf("same key: %s then %s", first, again)
	}
	if other == first {
		t.Errorf("another key gives the same fake %s", first)
	}
	if first == unkeyedFake(phoneA) || other == unkeyedFake(phoneA) {
		t.Errorf("fake %s is the unkeyed hash of the number", first)
	}
	if err := ingest.Scrub(in, t.TempDir(), []byte("short")); err == nil {
		t.Error("a short key was accepted")
	}
}

// Fix round 1, I3: the key file is created once with 32 random bytes and mode 0600, then reused.
func TestScrubKeyFileCreatedPrivateAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archiver", "scrub.key")
	key, err := ingest.LoadScrubKey(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || len(key) != 32 {
		t.Fatalf("key file %v err %v, key %d bytes; want 0600 and 32 bytes", info, err, len(key))
	}
	again, err := ingest.LoadScrubKey(path)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatalf("second load: err %v, same key %v", err, bytes.Equal(key, again))
	}
	other, _ := ingest.LoadScrubKey(filepath.Join(t.TempDir(), "k"))
	if bytes.Equal(key, other) {
		t.Fatal("two new key files hold the same key")
	}
	if err = os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.LoadScrubKey(path); err == nil {
		t.Fatal("a short key file was accepted")
	}
}
