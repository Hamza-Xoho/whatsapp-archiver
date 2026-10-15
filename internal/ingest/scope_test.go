package ingest_test

import (
	"errors"
	"testing"
	"whatsapp-archiver/internal/ingest"
)

// keep evaluates the filter and fails the test on an error: these cases have a working alias lookup.
func keep(t *testing.T, f *ingest.Filter, chat string, kind ingest.Kind) bool {
	t.Helper()
	ok, err := f.Keep(chat, kind)
	if err != nil {
		t.Fatalf("Keep(%s, %s): %v", chat, kind, err)
	}
	return ok
}

func TestScopeExcludesDirectAndAlias(t *testing.T) {
	f := ingest.NewFilter([]string{"1@s.whatsapp.net"}, func(j string) ([]string, error) {
		if j == "1@lid" {
			return []string{"1@s.whatsapp.net"}, nil
		}
		return nil, nil
	})
	if keep(t, f, "1@s.whatsapp.net", ingest.KindMessage) || keep(t, f, "1@lid", ingest.KindMessage) {
		t.Fatal("excluded chat kept")
	}
}

func TestScopeAlwaysSkipsNoise(t *testing.T) {
	f := ingest.NewFilter(nil, func(string) ([]string, error) { return nil, nil })
	for _, k := range []ingest.Kind{ingest.KindPresence, ingest.KindTyping, ingest.KindReceipt, ingest.KindCall} {
		if keep(t, f, "g@g.us", k) {
			t.Fatalf("kept %s", k)
		}
	}
	if keep(t, f, "status@broadcast", ingest.KindMessage) {
		t.Fatal("kept status")
	}
	if !keep(t, f, "g@g.us", ingest.KindGroupInfo) {
		t.Fatal("dropped group info")
	}
}

// A failed alias lookup is an error, never an answer: the filter must not guess "keep".
func TestScopeReportsAliasLookupError(t *testing.T) {
	boom := errors.New("database is locked")
	f := ingest.NewFilter([]string{"1@s.whatsapp.net"}, func(string) ([]string, error) { return nil, boom })
	ok, err := f.Keep("1@lid", ingest.KindMessage)
	if ok || !errors.Is(err, boom) {
		t.Fatalf("keep=%v err=%v", ok, err)
	}
}
