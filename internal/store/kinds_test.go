package store_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

// R6 / schema 4.2: the open-ended vocabularies are lists in Go, one each.
func TestKindListsMatchTheSchemaDesign(t *testing.T) {
	wantKinds := []string{"text", "image", "video", "audio", "voice", "document", "sticker", "gif", "location", "live_location",
		"contact", "contacts", "poll", "event", "system", "unknown"}
	for _, k := range wantKinds {
		if !store.ValidMessageKind(k) {
			t.Errorf("message kind %q is not valid", k)
		}
	}
	if len(store.MessageKinds) != len(wantKinds) {
		t.Errorf("MessageKinds = %v, want exactly %v", store.MessageKinds, wantKinds)
	}
	for _, k := range []string{"", "media", "ptt", "Text"} {
		if store.ValidMessageKind(k) {
			t.Errorf("message kind %q must be invalid", k)
		}
	}
	for _, k := range []string{"edit", "revoke", "protocol", "reaction", "pin", "poll_vote", "undecryptable", "secret_encrypted", "stub"} {
		if !store.ValidEventKind(k) {
			t.Errorf("event kind %q is not valid", k)
		}
	}
	if store.ValidEventKind("message") || store.ValidEventKind("") {
		t.Error("event kind message/empty must be invalid")
	}
	for _, c := range []string{"", "no_sender_key", "untrusted_identity", "view_once_unavailable", "other"} {
		if !store.ValidEventCause(c) {
			t.Errorf("event cause %q is not valid", c)
		}
	}
	if store.ValidEventCause("gremlins") {
		t.Error("event cause gremlins must be invalid")
	}
}

func TestValidateDetailsAcceptsTheDocumentedShapes(t *testing.T) {
	ok := map[string][]string{
		"text":          {`{}`, `{"link":{"url":"https://example.org","title":"T","description":"D"}}`},
		"image":         {`{}`},
		"video":         {`{}`, `{"ptv":true}`},
		"sticker":       {`{"animated":true}`},
		"location":      {`{"lat":24.7136,"lon":46.6753,"name":"Home","address":"Riyadh","url":"https://maps.example/x"}`},
		"live_location": {`{"lat":24.7,"lon":46.6,"accuracy_m":10,"speed":1.5,"sequence":3}`},
		"contact":       {`{"display_name":"Aisha","vcard":"BEGIN:VCARD"}`},
		"contacts":      {`{"display_name":"2 contacts","contacts":[{"display_name":"A","vcard":"x"},{"display_name":"B","vcard":"y"}]}`},
		"poll":          {`{"question":"Lunch?","options":["yes","no"],"max":1}`},
		"event":         {`{"name":"Dinner","description":"d","start_ts":1,"end_ts":2,"location":{"name":"Cafe","lat":1,"lon":2},"canceled":false}`, `{"name":"Dinner","location":null}`},
		"system":        {`{"type":"call_log"}`},
		"unknown":       {`{"fields":["interactiveMessage","x"]}`},
	}
	for kind, inputs := range ok {
		for _, in := range inputs {
			got, err := store.ValidateDetails(kind, in)
			if err != nil {
				t.Errorf("%s %s: %v", kind, in, err)
				continue
			}
			again, err := store.ValidateDetails(kind, got)
			if err != nil || again != got {
				t.Errorf("%s: canonical form %s is not stable (%s, %v)", kind, got, again, err)
			}
		}
	}
}

func TestValidateDetailsRejectsWhatTheKindDoesNotHave(t *testing.T) {
	bad := map[string][]string{
		"text":     {`{"animated":true}`, `{"link":{"url":"u","surprise":1}}`, `{"link":"x"}`},
		"image":    {`{"lat":1}`},
		"sticker":  {`{"animated":"yes"}`},
		"location": {`{"lat":"north"}`, `{"accuracy_m":3}`},
		"poll":     {`{"options":"a"}`, `{"question":1}`},
		"unknown":  {`{"fields":"a"}`},
	}
	for kind, inputs := range bad {
		for _, in := range inputs {
			if _, err := store.ValidateDetails(kind, in); err == nil {
				t.Errorf("%s %s was accepted", kind, in)
			}
		}
	}
	for _, in := range []string{`{`, `[]`, `"text"`, `null`, `{} {}`, `{"a":1}{"b":2}`} {
		if _, err := store.ValidateDetails("text", in); err == nil {
			t.Errorf("details %q was accepted", in)
		}
	}
	if _, err := store.ValidateDetails("carrier_pigeon", `{}`); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

// An empty string is "nothing extra"; the stored form is {} (the column is NOT NULL).
func TestValidateDetailsEmptyMeansEmptyObject(t *testing.T) {
	for _, in := range []string{"", "{}", "  {} "} {
		got, err := store.ValidateDetails("text", in)
		if err != nil || got != "{}" {
			t.Errorf("ValidateDetails(%q) = %q, %v; want {}", in, got, err)
		}
	}
}

// Marshalled deterministically: the same content in another key order gives the same bytes.
func TestValidateDetailsIsDeterministic(t *testing.T) {
	a, err := store.ValidateDetails("poll", `{"question":"Q","options":["a","b"],"max":2}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.ValidateDetails("poll", `{"max":2,"options":["a","b"],"question":"Q"}`)
	if err != nil || a != b {
		t.Fatalf("%s vs %s, %v", a, b, err)
	}
	if !strings.HasPrefix(a, `{"question"`) {
		t.Errorf("canonical form %s does not follow the documented field order", a)
	}
}

// Apply stores the canonical form and rejects a bad one before anything is written.
func TestApplyStoresCanonicalDetails(t *testing.T) {
	s := open(t)
	m := message("g@g.us", "P1", time.Unix(5, 0))
	m.Kind, m.Details = "poll", `{"max":1,"options":["a","b"],"question":"Q"}`
	if _, err := s.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	want, _ := store.ValidateDetails("poll", m.Details)
	if got := text(t, s, "SELECT details FROM messages"); got != want {
		t.Fatalf("details = %s, want %s", got, want)
	}
	if !slices.Contains(store.MessageKinds, "poll") {
		t.Fatal("poll missing from MessageKinds")
	}
}
