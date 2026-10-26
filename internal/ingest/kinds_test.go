package ingest_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

// W2(c): FromMessage fills every typed column of messages. Each case builds one proto message and names the kind, the
// text, the caption and the exact details JSON of docs/v1/schema.md section 4.2.

func normalized(msg *waE2E.Message) store.Message {
	r := ingest.FromMessage(messageEvent("g@g.us", "K1", msg), "live", time.Unix(20, 0))
	if r.Message == nil {
		panic("not a message")
	}
	return *r.Message
}

func TestEveryKindFillsKindTextCaptionAndDetails(t *testing.T) {
	vcard := "BEGIN:VCARD\nVERSION:3.0\nFN:Omar\nEND:VCARD"
	cases := []struct {
		name                      string
		msg                       *waE2E.Message
		kind, text, caption, want string
	}{
		{"text", text("hello"), "text", "hello", "", `{}`},
		{"text with link preview", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("see https://example.org/a"),
			MatchedText: proto.String("https://example.org/a"), Title: proto.String("Example"), Description: proto.String("An example")}}, "text", "see https://example.org/a", "",
			`{"link":{"url":"https://example.org/a","title":"Example","description":"An example"}}`},
		{"extended text without preview", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("plain")}}, "text", "plain", "", `{}`},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("a photo")}}, "image", "", "a photo", `{}`},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Caption: proto.String("a clip")}}, "video", "", "a clip", `{}`},
		{"video with gif playback", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true)}}, "gif", "", "", `{}`},
		{"video note", &waE2E.Message{PtvMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}}, "video", "", "", `{"ptv":true}`},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/mpeg")}}, "audio", "", "", `{}`},
		{"push to talk audio is a voice note", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true)}}, "voice", "", "", `{}`},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), Caption: proto.String("syllabus")}}, "document", "", "syllabus", `{}`},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}}, "sticker", "", "", `{"animated":false}`},
		{"animated sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), IsAnimated: proto.Bool(true)}}, "sticker", "", "", `{"animated":true}`},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(24.7136), DegreesLongitude: proto.Float64(46.6753),
			Name: proto.String("Riyadh"), Address: proto.String("King Fahd Rd"), URL: proto.String("https://maps.example/x")}}, "location", "", "",
			`{"lat":24.7136,"lon":46.6753,"name":"Riyadh","address":"King Fahd Rd","url":"https://maps.example/x"}`},
		{"live location", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(24.5), DegreesLongitude: proto.Float64(46.5),
			AccuracyInMeters: proto.Uint32(12), SpeedInMps: proto.Float32(3.5), SequenceNumber: proto.Int64(7), Caption: proto.String("on my way")}}, "live_location", "", "on my way",
			`{"lat":24.5,"lon":46.5,"accuracy_m":12,"speed":3.5,"sequence":7}`},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Omar"), Vcard: proto.String(vcard)}}, "contact", "Omar", "",
			`{"display_name":"Omar","vcard":"BEGIN:VCARD\nVERSION:3.0\nFN:Omar\nEND:VCARD"}`},
		{"contacts", &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{DisplayName: proto.String("2 contacts"), Contacts: []*waE2E.ContactMessage{
			{DisplayName: proto.String("A"), Vcard: proto.String("v1")}, {DisplayName: proto.String("B"), Vcard: proto.String("v2")}}}}, "contacts", "2 contacts", "",
			`{"display_name":"2 contacts","contacts":[{"display_name":"A","vcard":"v1"},{"display_name":"B","vcard":"v2"}]}`},
		{"poll", &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), SelectableOptionsCount: proto.Uint32(1),
			Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Yes")}, {OptionName: proto.String("No")}}}}, "poll", "Lunch?", "",
			`{"question":"Lunch?","options":["Yes","No"],"max":1}`},
		{"poll of a newer version", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("Where?"),
			Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Here")}, {OptionName: proto.String("There")}}}}, "poll", "Where?", "",
			`{"question":"Where?","options":["Here","There"]}`},
		{"poll in the V4 wrapper", &waE2E.Message{PollCreationMessageV4: &waE2E.FutureProofMessage{Message: &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Wrapped?"),
			Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("A")}}}}}}, "poll", "Wrapped?", "", `{"question":"Wrapped?","options":["A"]}`},
		{"event", &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Picnic"), Description: proto.String("Bring food"), StartTime: proto.Int64(1760003600),
			EndTime: proto.Int64(1760007200), Location: &waE2E.LocationMessage{Name: proto.String("Park"), DegreesLatitude: proto.Float64(24.5), DegreesLongitude: proto.Float64(46.5)}}}, "event", "Picnic", "",
			`{"name":"Picnic","description":"Bring food","start_ts":1760003600,"end_ts":1760007200,"location":{"name":"Park","lat":24.5,"lon":46.5},"canceled":false}`},
		{"canceled event without a place", &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Called off"), IsCanceled: proto.Bool(true)}}, "event", "Called off", "",
			`{"name":"Called off","canceled":true}`},
		{"call log", &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{}}, "system", "", "", `{"type":"callLogMesssage"}`},
		{"scheduled call", &waE2E.Message{ScheduledCallCreationMessage: &waE2E.ScheduledCallCreationMessage{Title: proto.String("Standup")}}, "system", "", "", `{"type":"scheduledCallCreationMessage"}`},
		{"unknown lists its proto fields", &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{GroupName: proto.String("g")}}, "unknown", "", "", `{"fields":["groupInviteMessage"]}`},
		{"unknown fields in field-number order", &waE2E.Message{OrderMessage: &waE2E.OrderMessage{}, GroupInviteMessage: &waE2E.GroupInviteMessage{}}, "unknown", "", "", `{"fields":["groupInviteMessage","orderMessage"]}`},
		{"bodyless message is unknown", &waE2E.Message{}, "unknown", "", "", `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := normalized(c.msg)
			if m.Kind != c.kind || m.Text != c.text || m.Caption != c.caption || m.Details != c.want {
				t.Fatalf("kind=%q text=%q caption=%q details=%s\nwant kind=%q text=%q caption=%q details=%s", m.Kind, m.Text, m.Caption, m.Details, c.kind, c.text, c.caption, c.want)
			}
			if canon, err := store.ValidateDetails(m.Kind, m.Details); err != nil || canon != m.Details {
				t.Fatalf("details %s are not canonical for kind %s: %q, %v", m.Details, m.Kind, canon, err)
			}
		})
	}
}

// Every kind of the store's list is produced by some case above, so adding a kind to store/kinds.go without a mapping fails here.
func TestEveryStoredKindHasAMapping(t *testing.T) {
	produced := map[string]bool{}
	for _, msg := range []*waE2E.Message{text("a"), {ImageMessage: &waE2E.ImageMessage{}}, {VideoMessage: &waE2E.VideoMessage{}}, {AudioMessage: &waE2E.AudioMessage{}},
		{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}, {DocumentMessage: &waE2E.DocumentMessage{}}, {StickerMessage: &waE2E.StickerMessage{}},
		{VideoMessage: &waE2E.VideoMessage{GifPlayback: proto.Bool(true)}}, {LocationMessage: &waE2E.LocationMessage{}}, {LiveLocationMessage: &waE2E.LiveLocationMessage{}},
		{ContactMessage: &waE2E.ContactMessage{}}, {ContactsArrayMessage: &waE2E.ContactsArrayMessage{}}, {PollCreationMessage: &waE2E.PollCreationMessage{}},
		{EventMessage: &waE2E.EventMessage{}}, {CallLogMesssage: &waE2E.CallLogMessage{}}, {}} {
		produced[normalized(msg).Kind] = true
	}
	for _, k := range store.MessageKinds {
		if !produced[k] {
			t.Errorf("kind %q is never produced by the normaliser", k)
		}
	}
}

func TestMediaFactsKnownAtEnqueueGoOnTheMediaRow(t *testing.T) {
	thumb := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}
	cases := []struct {
		name string
		msg  *waE2E.Message
		want store.Media
	}{
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(2048), Width: proto.Uint32(640), Height: proto.Uint32(480), JPEGThumbnail: thumb}},
			store.Media{Type: "image", MIME: "image/jpeg", DeclaredSize: 2048, Width: 640, Height: 480, Thumb: thumb}},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), FileLength: proto.Uint64(9000), Seconds: proto.Uint32(31), Width: proto.Uint32(720), Height: proto.Uint32(1280), JPEGThumbnail: thumb}},
			store.Media{Type: "video", MIME: "video/mp4", DeclaredSize: 9000, DurationS: 31, Width: 720, Height: 1280, Thumb: thumb}},
		{"gif", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true), Seconds: proto.Uint32(3)}},
			store.Media{Type: "gif", MIME: "video/mp4", DurationS: 3}},
		{"voice", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(12), FileLength: proto.Uint64(5000)}},
			store.Media{Type: "voice", MIME: "audio/ogg; codecs=opus", DeclaredSize: 5000, DurationS: 12}},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("syllabus.pdf"), PageCount: proto.Uint32(14), FileLength: proto.Uint64(77000), JPEGThumbnail: thumb}},
			store.Media{Type: "document", MIME: "application/pdf", DeclaredSize: 77000, Filename: "syllabus.pdf", Pages: 14, Thumb: thumb}},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), Width: proto.Uint32(512), Height: proto.Uint32(512), PngThumbnail: thumb}},
			store.Media{Type: "sticker", MIME: "image/webp", Width: 512, Height: 512, Thumb: thumb}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ingest.FromMessage(messageEvent("g@g.us", "M1", c.msg), "history", time.Unix(20, 0))
			got := r.Media
			if got == nil {
				t.Fatal("no media row")
			}
			c.want.ChatJID, c.want.MessageID, c.want.SenderJID, c.want.Source, c.want.State, c.want.MessageTimestamp = "g@g.us", "M1", "2@lid", "history", "pending", 10
			if got.Type != c.want.Type || got.MIME != c.want.MIME || got.DeclaredSize != c.want.DeclaredSize || got.Filename != c.want.Filename || got.DurationS != c.want.DurationS ||
				got.Width != c.want.Width || got.Height != c.want.Height || got.Pages != c.want.Pages || string(got.Thumb) != string(c.want.Thumb) ||
				got.ChatJID != c.want.ChatJID || got.MessageID != c.want.MessageID || got.Source != c.want.Source || got.State != "pending" || got.MessageTimestamp != 10 {
				t.Fatalf("media=%+v\nwant %+v", *got, c.want)
			}
		})
	}
}

func TestMessagesWithoutAFileHaveNoMediaRow(t *testing.T) {
	for _, msg := range []*waE2E.Message{text("a"), {LocationMessage: &waE2E.LocationMessage{}}, {ContactMessage: &waE2E.ContactMessage{}}, {PollCreationMessage: &waE2E.PollCreationMessage{}}, {}} {
		if r := ingest.FromMessage(messageEvent("g@g.us", "N1", msg), "live", time.Now()); r.Media != nil {
			t.Fatalf("media row %+v for %v", *r.Media, msg)
		}
	}
}

// An absent proto field is an absent key (store/kinds.go): zero is a real value for a coordinate, so only a field that
// is set is written.
func TestAbsentPlaceFieldsAreOmitted(t *testing.T) {
	for _, c := range []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"location without coordinates", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{Name: proto.String("Somewhere")}}, `{"name":"Somewhere"}`},
		{"location on the equator", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(0), DegreesLongitude: proto.Float64(0)}}, `{"lat":0,"lon":0}`},
		{"live location with a position only", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(24.5), DegreesLongitude: proto.Float64(46.5)}}, `{"lat":24.5,"lon":46.5}`},
		{"live location with a zero speed", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{SpeedInMps: proto.Float32(0), SequenceNumber: proto.Int64(0)}}, `{"speed":0,"sequence":0}`},
		{"event place with a name only", &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("e"), Location: &waE2E.LocationMessage{Name: proto.String("Park")}}}, `{"name":"e","location":{"name":"Park"},"canceled":false}`},
	} {
		if m := normalized(c.msg); m.Details != c.want {
			t.Errorf("%s: details=%s, want %s", c.name, m.Details, c.want)
		}
	}
}
