package ingest

import (
	"sort"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/reflect/protoreflect"

	"whatsapp-archiver/internal/store"
)

// normalizeContent turns the message into an event (reaction, edit, revoke, ...) or fills every typed column of the
// message row: kind, text, caption, details, the context (quote, mentions, forwarding, expiry), view-once and, for a
// file, the media row. msg is the unwrapped message, raw the message as received.
func normalizeContent(evt *events.Message, raw *waE2E.Message, r *Result) {
	msg := evt.Message
	if msg == nil {
		r.Message.Kind, r.Message.Details = "unknown", "{}"
		return
	}
	if inner := msg.GetEditedMessage().GetMessage(); inner != nil {
		msg = inner // the EditedMessage wrapper (whatsmeow's BuildEdit) holds the MESSAGE_EDIT protocol message
	}
	if normalizeEvent(msg, r) {
		return
	}
	fillMessage(evt, raw, msg, r)
}

// noContext is the leaf of a message that has no context info (a plain conversation).
type noContext struct{}

// fillMessage sets the columns of a message row from its content. Each group of kinds returns the proto message it
// matched (its leaf), which holds the context info and the view-once flag, or nil when it matched nothing.
func fillMessage(evt *events.Message, raw, msg *waE2E.Message, r *Result) {
	m := r.Message
	m.Kind, m.Details = "unknown", "{}"
	var leaf any
	for _, group := range []func(*waE2E.Message, *Result) any{textContent, fileContent, placeContent, contactContent, structuredContent, systemContent} {
		if leaf = group(msg, r); leaf != nil {
			break
		}
	}
	if leaf == nil {
		m.Details = jsonOf(store.UnknownDetails{Fields: protoFields(msg)})
		return
	}
	if c, ok := leaf.(interface{ GetContextInfo() *waE2E.ContextInfo }); ok {
		contextFields(m, c.GetContextInfo())
	}
	flag, _ := leaf.(interface{ GetViewOnce() bool })
	m.ViewOnce = evt.IsViewOnce || (flag != nil && flag.GetViewOnce()) || hasViewOnceWrapper(raw)
}

// contextFields copies the quote, the mentions as received (CON-7), the forwarding and the expiry of a context.
func contextFields(m *store.Message, c *waE2E.ContextInfo) {
	if c == nil {
		return
	}
	m.QuotedID, m.QuotedSender = c.GetStanzaID(), c.GetParticipant()
	m.Mentions = append([]string(nil), c.GetMentionedJID()...)
	m.ForwardScore = int(c.GetForwardingScore())
	m.Forwarded = c.GetIsForwarded() || m.ForwardScore > 0
	m.EphemeralSecs = int(c.GetExpiration())
}

func hasViewOnceWrapper(msg *waE2E.Message) bool {
	return msg.GetViewOnceMessage() != nil || msg.GetViewOnceMessageV2() != nil || msg.GetViewOnceMessageV2Extension() != nil
}

func jsonOf(v any) string {
	s, err := store.MarshalDetails(v)
	if err != nil {
		return "{}"
	}
	return s
}

func textContent(msg *waE2E.Message, r *Result) any {
	m := r.Message
	if msg.Conversation != nil {
		m.Kind, m.Text = "text", msg.GetConversation()
		return noContext{}
	}
	x := msg.GetExtendedTextMessage()
	if x == nil {
		return nil
	}
	m.Kind, m.Text = "text", x.GetText()
	var d store.TextDetails
	if x.GetMatchedText() != "" || x.GetTitle() != "" || x.GetDescription() != "" {
		d.Link = &store.LinkDetails{URL: x.GetMatchedText(), Title: x.GetTitle(), Description: x.GetDescription()}
	}
	m.Details = jsonOf(d)
	return x
}

// mediaFacts are what the proto says about a file when it is enqueued; the file itself is downloaded later.
type mediaFacts struct {
	mime                          string
	size                          uint64
	filename                      string
	seconds, width, height, pages uint32
	thumb                         []byte
}

func fileContent(msg *waE2E.Message, r *Result) any {
	m := r.Message
	switch {
	case msg.GetImageMessage() != nil:
		x := msg.GetImageMessage()
		m.Caption = x.GetCaption()
		setMedia(r, "image", mediaFacts{mime: x.GetMimetype(), size: x.GetFileLength(), width: x.GetWidth(), height: x.GetHeight(), thumb: x.GetJPEGThumbnail()})
		return x
	case msg.GetVideoMessage() != nil:
		return videoContent(msg.GetVideoMessage(), r, false)
	case msg.GetPtvMessage() != nil:
		return videoContent(msg.GetPtvMessage(), r, true)
	case msg.GetAudioMessage() != nil:
		x := msg.GetAudioMessage()
		kind := "audio"
		if x.GetPTT() {
			kind = "voice"
		}
		setMedia(r, kind, mediaFacts{mime: x.GetMimetype(), size: x.GetFileLength(), seconds: x.GetSeconds()})
		return x
	case msg.GetDocumentMessage() != nil:
		x := msg.GetDocumentMessage()
		m.Caption = x.GetCaption()
		name := x.GetFileName()
		if name == "" {
			name = x.GetTitle()
		}
		setMedia(r, "document", mediaFacts{mime: x.GetMimetype(), size: x.GetFileLength(), filename: name, pages: x.GetPageCount(), thumb: x.GetJPEGThumbnail()})
		return x
	case msg.GetStickerMessage() != nil:
		x := msg.GetStickerMessage()
		setMedia(r, "sticker", mediaFacts{mime: x.GetMimetype(), size: x.GetFileLength(), width: x.GetWidth(), height: x.GetHeight(), thumb: x.GetPngThumbnail()})
		animated := x.GetIsAnimated()
		m.Details = jsonOf(store.StickerDetails{Animated: &animated})
		return x
	}
	return nil
}

func videoContent(x *waE2E.VideoMessage, r *Result, note bool) any {
	kind := "video"
	if x.GetGifPlayback() {
		kind = "gif"
	}
	r.Message.Caption = x.GetCaption()
	setMedia(r, kind, mediaFacts{mime: x.GetMimetype(), size: x.GetFileLength(), seconds: x.GetSeconds(), width: x.GetWidth(), height: x.GetHeight(), thumb: x.GetJPEGThumbnail()})
	if note && kind == "video" {
		ptv := true
		r.Message.Details = jsonOf(store.VideoDetails{PTV: &ptv})
	}
	return x
}

// setMedia makes the message a media message of the kind and describes its queue row; the raw proto is not copied into
// the row (R4), the downloader reads messages.raw_proto. The store ranks the row by source.
func setMedia(r *Result, kind string, f mediaFacts) {
	r.Message.Kind = kind
	r.Media = &store.Media{ChatJID: r.Message.ChatJID, MessageID: r.Message.ID, SenderJID: r.Message.SenderJID, Source: r.Message.Source, Type: kind, MIME: f.mime,
		DeclaredSize: int64(f.size), Filename: f.filename, DurationS: int(f.seconds), Width: int(f.width), Height: int(f.height), Pages: int(f.pages),
		Thumb: f.thumb, State: "pending", MessageTimestamp: r.Message.Timestamp, FromMe: r.FromMe}
}

func placeContent(msg *waE2E.Message, r *Result) any {
	m := r.Message
	if x := msg.GetLocationMessage(); x != nil {
		lat, lon := x.GetDegreesLatitude(), x.GetDegreesLongitude()
		m.Kind = "location"
		m.Details = jsonOf(store.LocationDetails{Lat: &lat, Lon: &lon, Name: x.GetName(), Address: x.GetAddress(), URL: x.GetURL()})
		return x
	}
	if x := msg.GetLiveLocationMessage(); x != nil {
		lat, lon, accuracy, speed, seq := x.GetDegreesLatitude(), x.GetDegreesLongitude(), float64(x.GetAccuracyInMeters()), float64(x.GetSpeedInMps()), x.GetSequenceNumber()
		m.Kind, m.Caption = "live_location", x.GetCaption()
		m.Details = jsonOf(store.LiveLocationDetails{Lat: &lat, Lon: &lon, AccuracyM: &accuracy, Speed: &speed, Sequence: &seq})
		return x
	}
	return nil
}

func contactContent(msg *waE2E.Message, r *Result) any {
	m := r.Message
	if x := msg.GetContactMessage(); x != nil {
		m.Kind, m.Text = "contact", x.GetDisplayName()
		m.Details = jsonOf(store.ContactDetails{DisplayName: x.GetDisplayName(), VCard: x.GetVcard()})
		return x
	}
	if x := msg.GetContactsArrayMessage(); x != nil {
		d := store.ContactsDetails{DisplayName: x.GetDisplayName()}
		for _, c := range x.GetContacts() {
			d.Contacts = append(d.Contacts, store.ContactDetails{DisplayName: c.GetDisplayName(), VCard: c.GetVcard()})
		}
		m.Kind, m.Text, m.Details = "contacts", x.GetDisplayName(), jsonOf(d)
		return x
	}
	return nil
}

// pollOf returns the poll creation message of msg in any of its versions.
func pollOf(msg *waE2E.Message) *waE2E.PollCreationMessage {
	for _, p := range []*waE2E.PollCreationMessage{msg.GetPollCreationMessage(), msg.GetPollCreationMessageV2(), msg.GetPollCreationMessageV3(),
		msg.GetPollCreationMessageV5(), msg.GetPollCreationMessageV6()} {
		if p != nil {
			return p
		}
	}
	if inner := msg.GetPollCreationMessageV4().GetMessage(); inner != nil {
		return pollOf(inner)
	}
	return nil
}

// pollOptions lists the option names of a poll.
func pollOptions(p *waE2E.PollCreationMessage) []string {
	var out []string
	for _, o := range p.GetOptions() {
		out = append(out, o.GetOptionName())
	}
	return out
}

func structuredContent(msg *waE2E.Message, r *Result) any {
	m := r.Message
	if x := pollOf(msg); x != nil {
		d := store.PollDetails{Question: x.GetName(), Options: pollOptions(x)}
		if x.SelectableOptionsCount != nil {
			max := int(x.GetSelectableOptionsCount())
			d.Max = &max
		}
		m.Kind, m.Text, m.Details = "poll", x.GetName(), jsonOf(d)
		return x
	}
	if x := msg.GetEventMessage(); x != nil {
		canceled := x.GetIsCanceled()
		d := store.EventDetails{Name: x.GetName(), Description: x.GetDescription(), Canceled: &canceled}
		if x.GetStartTime() != 0 {
			start := x.GetStartTime()
			d.StartTS = &start
		}
		if x.GetEndTime() != 0 {
			end := x.GetEndTime()
			d.EndTS = &end
		}
		if loc := x.GetLocation(); loc != nil {
			lat, lon := loc.GetDegreesLatitude(), loc.GetDegreesLongitude()
			d.Location = &store.EventPlace{Name: loc.GetName(), Lat: &lat, Lon: &lon}
		}
		m.Kind, m.Text, m.Details = "event", x.GetName(), jsonOf(d)
		return x
	}
	return nil
}

// systemContent matches the notices that are not a person's message: call logs and scheduled calls.
func systemContent(msg *waE2E.Message, r *Result) any {
	var field string
	switch {
	case msg.GetCall() != nil:
		field = "call"
	case msg.GetCallLogMesssage() != nil:
		field = "callLogMesssage"
	case msg.GetScheduledCallCreationMessage() != nil:
		field = "scheduledCallCreationMessage"
	case msg.GetScheduledCallEditMessage() != nil:
		field = "scheduledCallEditMessage"
	case msg.GetBcallMessage() != nil:
		field = "bcallMessage"
	default:
		return nil
	}
	r.Message.Kind, r.Message.Details = "system", jsonOf(store.SystemDetails{Type: field})
	return noContext{}
}

// protoFields lists the top-level proto fields set on msg by field number, leaving out the ones every message has.
func protoFields(msg *waE2E.Message) []string {
	type field struct {
		num  protoreflect.FieldNumber
		name string
	}
	var fields []field
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		switch fd.Name() {
		case "messageContextInfo", "senderKeyDistributionMessage", "fastRatchetKeySenderKeyDistributionMessage":
		default:
			fields = append(fields, field{fd.Number(), string(fd.Name())})
		}
		return true
	})
	sort.Slice(fields, func(i, j int) bool { return fields[i].num < fields[j].num })
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.name
	}
	return out
}
