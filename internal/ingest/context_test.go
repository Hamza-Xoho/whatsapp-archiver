package ingest_test

import (
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func extended(body string, ctx *waE2E.ContextInfo) *waE2E.Message {
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(body), ContextInfo: ctx}}
}

func TestQuotedReplyKeepsIDAndSender(t *testing.T) {
	m := normalized(extended("reply", &waE2E.ContextInfo{StanzaID: proto.String("Q1"), Participant: proto.String("20000000001@lid")}))
	if m.QuotedID != "Q1" || m.QuotedSender != "20000000001@lid" {
		t.Fatalf("quoted=%q/%q", m.QuotedID, m.QuotedSender)
	}
}

// The context of every kind is read, not only that of text, image, video and document.
func TestContextIsReadForEveryKindThatHasOne(t *testing.T) {
	ctx := &waE2E.ContextInfo{StanzaID: proto.String("Q9"), Participant: proto.String("3@lid"), ForwardingScore: proto.Uint32(2), Expiration: proto.Uint32(86400), MentionedJID: []string{"3@lid"}}
	for name, msg := range map[string]*waE2E.Message{
		"audio":    {AudioMessage: &waE2E.AudioMessage{ContextInfo: ctx}},
		"sticker":  {StickerMessage: &waE2E.StickerMessage{ContextInfo: ctx}},
		"location": {LocationMessage: &waE2E.LocationMessage{ContextInfo: ctx}},
		"contact":  {ContactMessage: &waE2E.ContactMessage{ContextInfo: ctx}},
		"contacts": {ContactsArrayMessage: &waE2E.ContactsArrayMessage{ContextInfo: ctx}},
		"poll":     {PollCreationMessage: &waE2E.PollCreationMessage{ContextInfo: ctx}},
		"event":    {EventMessage: &waE2E.EventMessage{ContextInfo: ctx}},
		"live":     {LiveLocationMessage: &waE2E.LiveLocationMessage{ContextInfo: ctx}},
		"video":    {PtvMessage: &waE2E.VideoMessage{ContextInfo: ctx}},
	} {
		m := normalized(msg)
		if m.QuotedID != "Q9" || m.QuotedSender != "3@lid" || !m.Forwarded || m.ForwardScore != 2 || m.EphemeralSecs != 86400 || !slices.Equal(m.Mentions, []string{"3@lid"}) {
			t.Errorf("%s: %+v", name, m)
		}
	}
}

func TestForwardedFlagAndScore(t *testing.T) {
	for _, c := range []struct {
		name      string
		ctx       *waE2E.ContextInfo
		forwarded bool
		score     int
	}{
		{"none", &waE2E.ContextInfo{}, false, 0},
		{"flag without a score", &waE2E.ContextInfo{IsForwarded: proto.Bool(true)}, true, 0},
		{"score of one", &waE2E.ContextInfo{ForwardingScore: proto.Uint32(1)}, true, 1},
		{"forwarded many times", &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(127)}, true, 127},
	} {
		if m := normalized(extended("x", c.ctx)); m.Forwarded != c.forwarded || m.ForwardScore != c.score {
			t.Errorf("%s: forwarded=%v score=%d", c.name, m.Forwarded, m.ForwardScore)
		}
	}
}

// CON-7: the mentioned JIDs are kept exactly as received (LID and phone forms side by side), in order.
func TestMentionsAreKeptAsReceived(t *testing.T) {
	mentions := []string{"20000000001@lid", "10000000002@s.whatsapp.net", "20000000001@lid"}
	m := normalized(extended("@a @b @a", &waE2E.ContextInfo{MentionedJID: mentions}))
	if !slices.Equal(m.Mentions, mentions) {
		t.Fatalf("mentions=%v", m.Mentions)
	}
	if m := normalized(text("no mentions")); len(m.Mentions) != 0 {
		t.Fatalf("mentions=%v", m.Mentions)
	}
}

// Arabic text with diacritics, tatweel and an RTL mention is stored byte for byte; folding is the search index's job.
func TestArabicTextIsStoredVerbatim(t *testing.T) {
	body := "السَّلامُ عَلَيْكُمْ يا @٢٠٠٠٠٠٠٠٠٠٠١ في المجمـــوعة‏"
	m := normalized(extended(body, &waE2E.ContextInfo{MentionedJID: []string{"20000000001@lid"}}))
	if m.Text != body || m.Kind != "text" || !slices.Equal(m.Mentions, []string{"20000000001@lid"}) {
		t.Fatalf("text=%q mentions=%v", m.Text, m.Mentions)
	}
}

func TestEphemeralSecondsComeFromTheContext(t *testing.T) {
	if m := normalized(extended("x", &waE2E.ContextInfo{Expiration: proto.Uint32(604800)})); m.EphemeralSecs != 604800 {
		t.Fatalf("ephemeral=%d", m.EphemeralSecs)
	}
	if m := normalized(text("x")); m.EphemeralSecs != 0 {
		t.Fatalf("ephemeral=%d", m.EphemeralSecs)
	}
}

func TestViewOnceIsKeptForWrappersAndFlags(t *testing.T) {
	image := &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}
	for name, msg := range map[string]*waE2E.Message{
		"V1 wrapper":        {ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: image}}},
		"V2 wrapper":        {ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: image}}},
		"V2 extension":      {ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: image}}},
		"flag on the image": {ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), ViewOnce: proto.Bool(true)}},
	} {
		evt := (&events.Message{Info: messageEvent("g@g.us", "VO", nil).Info, RawMessage: msg}).UnwrapRaw()
		r := ingest.FromMessage(evt, "live", time.Unix(20, 0))
		if r.Message == nil || !r.Message.ViewOnce || r.Message.Kind != "image" || r.Media == nil {
			t.Errorf("%s: %+v", name, r.Message)
		}
	}
	if m := normalized(&waE2E.Message{ImageMessage: image}); m.ViewOnce {
		t.Fatal("an ordinary image is not view-once")
	}
}

func TestEphemeralWrapperIsUnwrappedForTheContext(t *testing.T) {
	inner := extended("vanishing", &waE2E.ContextInfo{Expiration: proto.Uint32(86400)})
	evt := (&events.Message{Info: messageEvent("g@g.us", "EP", nil).Info, RawMessage: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: inner}}}).UnwrapRaw()
	r := ingest.FromMessage(evt, "live", time.Unix(20, 0))
	if r.Message.Text != "vanishing" || r.Message.EphemeralSecs != 86400 {
		t.Fatalf("message=%+v", r.Message)
	}
}

func TestFromMeAndPushNameAreOnTheMessage(t *testing.T) {
	evt := messageEvent("g@g.us", "FM", text("mine"))
	evt.Info.IsFromMe, evt.Info.PushName = true, "Hamza"
	r := ingest.FromMessage(evt, "live", time.Unix(20, 0))
	if !r.Message.FromMe || r.Message.PushName != "Hamza" || r.Sender.PushName != "Hamza" {
		t.Fatalf("message=%+v sender=%+v", r.Message, r.Sender)
	}
}

// Every details string the normaliser writes passes the store's validation when it goes through Apply.
func TestNormalisedMessagesPassStoreValidation(t *testing.T) {
	st := testkit.Store(t)
	for i, msg := range []*waE2E.Message{text("a"), {StickerMessage: &waE2E.StickerMessage{IsAnimated: proto.Bool(true)}}, {PtvMessage: &waE2E.VideoMessage{}},
		{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2)}}, {EventMessage: &waE2E.EventMessage{Name: proto.String("e")}}, {}} {
		r := ingest.FromMessage(messageEvent("g@g.us", string(rune('A'+i)), msg), "live", time.Unix(20, 0))
		if _, err := st.Apply(t.Context(), store.Batch{Message: r.Message, Media: r.Media}); err != nil {
			t.Errorf("message %d: %v", i, err)
		}
	}
}
