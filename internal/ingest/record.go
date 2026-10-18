package ingest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Recorder writes each supported event to <dir>/<seq>-<kind>.json before passing it on.
type Recorder struct {
	dir    string
	log    *slog.Logger
	filter *Filter // nil records every chat
	mu     sync.Mutex
	seq    int
}

// recorded is one event file. Info is the event's metadata as JSON; Proto is its protobuf payload, base64.
type recorded struct {
	Kind  string          `json:"kind"`
	Info  json.RawMessage `json:"info,omitempty"`
	Proto *string         `json:"proto,omitempty"`
}

// appStateInfo is the Info of an archive, mute or pin event; its Action is the Proto.
type appStateInfo struct {
	JID          types.JID `json:"jid"`
	Timestamp    time.Time `json:"timestamp"`
	FromFullSync bool      `json:"from_full_sync"`
}

// protoKinds names the protobuf type of each kind that has a Proto.
var protoKinds = map[string]func() proto.Message{
	"message":      func() proto.Message { return &waE2E.Message{} },
	"history_sync": func() proto.Message { return &waHistorySync.HistorySync{} },
	"archive":      func() proto.Message { return &waSyncAction.ArchiveChatAction{} },
	"mute":         func() proto.Message { return &waSyncAction.MuteAction{} },
	"pin":          func() proto.Message { return &waSyncAction.PinAction{} },
}

func NewRecorder(dir string) *Recorder { return &Recorder{dir: dir, log: slog.Default()} }

// WithLogger sets where write failures are logged.
func (r *Recorder) WithLogger(log *slog.Logger) *Recorder {
	r.log = log
	return r
}

// WithFilter keeps the events of excluded chats out of the recording: an event is written only when the filter keeps
// its chat, and an event whose exclusion cannot be checked (a failed alias lookup) is not written either. Without a
// filter every supported event is written.
func (r *Recorder) WithFilter(f *Filter) *Recorder {
	r.filter = f
	return r
}

// Wrap records each supported event, then calls next with the same event and returns next's verdict; a recording
// failure is only logged.
func (r *Recorder) Wrap(next func(any) bool) func(any) bool {
	return func(evt any) bool {
		if err := r.record(evt); err != nil {
			r.log.Error("event not recorded", "type", fmt.Sprintf("%T", evt), "err", err)
		}
		return next(evt)
	}
}

func (r *Recorder) record(evt any) error {
	kind, info, payload := encodeEvent(evt)
	if kind == "" {
		return nil
	}
	payload, write, err := r.scope(evt, payload)
	if err != nil || !write {
		return err
	}
	item := recorded{Kind: kind}
	if info != nil {
		data, err := json.Marshal(info)
		if err != nil {
			return err
		}
		item.Info = data
	}
	if payload != nil && payload.ProtoReflect().IsValid() {
		raw, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(payload) // a chunk may lack required fields
		if err != nil {
			return err
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		item.Proto = &encoded
	}
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err = os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	if r.seq == 0 {
		r.seq = lastSeq(r.dir)
	}
	r.seq++
	return os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("%06d-%s.json", r.seq, kind)), data, 0o600)
}

// scope decides what of an event may be written: nothing when its chat is excluded, otherwise the payload to write.
// A history chunk is written without the conversations of excluded chats. An event whose chat cannot be determined
// is written as it is. Without a filter everything is written.
func (r *Recorder) scope(evt any, payload proto.Message) (proto.Message, bool, error) {
	if r.filter == nil {
		return payload, true, nil
	}
	if h, ok := evt.(*events.HistorySync); ok {
		return r.withoutExcludedConversations(h.Data)
	}
	chat, alias := eventChat(evt)
	if chat.IsEmpty() {
		return payload, true, nil
	}
	excluded, err := r.excluded(chat.String(), alias.String())
	if err != nil {
		return nil, false, err
	}
	return payload, !excluded, nil
}

// eventChat returns the chat an event belongs to, and the other form of that chat (the LID or phone number) that the
// event's message info names. An event without a chat returns empty JIDs.
func eventChat(evt any) (chat, alias types.JID) {
	switch e := evt.(type) {
	case *events.Message:
		return e.Info.Chat, e.Info.RecipientAlt
	case *events.UndecryptableMessage:
		return e.Info.Chat, e.Info.RecipientAlt
	case *events.GroupInfo:
		return e.JID, types.JID{}
	case *events.JoinedGroup:
		return e.JID, types.JID{}
	case *events.Archive:
		return e.JID, types.JID{}
	case *events.Mute:
		return e.JID, types.JID{}
	case *events.Pin:
		return e.JID, types.JID{}
	case *events.MediaRetry:
		return e.ChatID, types.JID{}
	}
	return types.JID{}, types.JID{}
}

// excluded reports whether any of the chats is excluded, by the filter's list or alias lookup. Empty chats are skipped.
func (r *Recorder) excluded(chats ...string) (bool, error) {
	for _, chat := range chats {
		if chat == "" {
			continue
		}
		ok, err := r.filter.Excluded(chat)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// withoutExcludedConversations returns the history chunk without the conversations of excluded chats and without the
// phone-number mappings that name one; the chunk itself when nothing is excluded. A conversation is excluded by its own
// ID or by a phone-number mapping the chunk carries, since the pipeline learns those mappings before it reads the
// conversations.
func (r *Recorder) withoutExcludedConversations(hs *waHistorySync.HistorySync) (proto.Message, bool, error) {
	if hs == nil {
		return nil, true, nil
	}
	var kept []*waHistorySync.Conversation
	dropped := false
	for _, conv := range hs.GetConversations() {
		excluded, err := r.excluded(append([]string{conv.GetID()}, historyAliases(hs, conv.GetID())...)...)
		if err != nil {
			return nil, false, err
		}
		if excluded {
			dropped = true
			continue
		}
		kept = append(kept, conv)
	}
	var mappings []*waHistorySync.PhoneNumberToLIDMapping
	for _, m := range hs.GetPhoneNumberToLidMappings() {
		excluded, err := r.excluded(m.GetPnJID(), m.GetLidJID())
		if err != nil {
			return nil, false, err
		}
		if excluded {
			dropped = true
			continue
		}
		mappings = append(mappings, m)
	}
	if !dropped {
		return hs, true, nil
	}
	out := proto.Clone(hs).(*waHistorySync.HistorySync)
	out.Conversations = kept
	out.PhoneNumberToLidMappings = mappings
	return out, true, nil
}

// historyAliases returns the other form of chat according to the chunk's own phone-number mappings.
func historyAliases(hs *waHistorySync.HistorySync, chat string) []string {
	if chat == "" {
		return nil
	}
	var out []string
	for _, m := range hs.GetPhoneNumberToLidMappings() {
		switch chat {
		case m.GetLidJID():
			out = append(out, m.GetPnJID())
		case m.GetPnJID():
			out = append(out, m.GetLidJID())
		}
	}
	return out
}

// lastSeq is the highest sequence number already in dir, so a later recorder does not overwrite earlier files.
func lastSeq(dir string) int {
	entries, _ := os.ReadDir(dir)
	last := 0
	for _, e := range entries {
		prefix, _, _ := strings.Cut(e.Name(), "-")
		if n, err := strconv.Atoi(prefix); err == nil && n > last {
			last = n
		}
	}
	return last
}

// encodeEvent returns the kind, the JSON metadata and the protobuf payload of a supported event; kind is "" otherwise.
func encodeEvent(evt any) (kind string, info any, payload proto.Message) {
	switch e := evt.(type) {
	case *events.Message:
		raw := e.RawMessage
		if raw == nil {
			raw = e.Message
		}
		return "message", e.Info, raw
	case *events.UndecryptableMessage:
		return "undecryptable", e, nil
	case *events.HistorySync:
		return "history_sync", nil, e.Data
	case *events.GroupInfo:
		return "group_info", e, nil
	case *events.JoinedGroup:
		return "joined_group", e, nil
	case *events.Archive:
		return "archive", appStateInfo{e.JID, e.Timestamp, e.FromFullSync}, e.Action
	case *events.Mute:
		return "mute", appStateInfo{e.JID, e.Timestamp, e.FromFullSync}, e.Action
	case *events.Pin:
		return "pin", appStateInfo{e.JID, e.Timestamp, e.FromFullSync}, e.Action
	case *events.MediaRetry:
		return "media_retry", e, nil
	}
	return "", nil, nil
}
