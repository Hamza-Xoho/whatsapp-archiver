package ingest_test

import (
	"testing"
	"whatsapp-archiver/internal/ingest"
)

func TestScopeExcludesDirectAndAlias(t *testing.T) {
	f := ingest.NewFilter([]string{"1@s.whatsapp.net"}, func(j string) []string {
		if j == "1@lid" {
			return []string{"1@s.whatsapp.net"}
		}
		return nil
	})
	if f.Keep("1@s.whatsapp.net", ingest.KindMessage) || f.Keep("1@lid", ingest.KindMessage) {
		t.Fatal("excluded chat kept")
	}
}

func TestScopeAlwaysSkipsNoise(t *testing.T) {
	f := ingest.NewFilter(nil, func(string) []string { return nil })
	for _, k := range []ingest.Kind{ingest.KindPresence, ingest.KindTyping, ingest.KindReceipt, ingest.KindCall} {
		if f.Keep("g@g.us", k) {
			t.Fatalf("kept %s", k)
		}
	}
	if f.Keep("status@broadcast", ingest.KindMessage) {
		t.Fatal("kept status")
	}
	if !f.Keep("g@g.us", ingest.KindGroupInfo) {
		t.Fatal("dropped group info")
	}
}
