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
	dir string
	log *slog.Logger
	mu  sync.Mutex
	seq int
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

// Wrap records each supported event, then calls next with the same event; a recording failure is only logged.
func (r *Recorder) Wrap(next func(any)) func(any) {
	return func(evt any) {
		if err := r.record(evt); err != nil {
			r.log.Error("event not recorded", "type", fmt.Sprintf("%T", evt), "err", err)
		}
		next(evt)
	}
}

func (r *Recorder) record(evt any) error {
	kind, info, payload := encodeEvent(evt)
	if kind == "" {
		return nil
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
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
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
