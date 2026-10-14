package ingest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

// Load rebuilds the recorded events in dir, in file-name order, as the Go event types Handle routes.
func Load(dir string) ([]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []any
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var item recorded
		if err = json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		evt, err := decodeEvent(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, evt)
	}
	return out, nil
}

func decodeEvent(r recorded) (any, error) {
	payload, err := decodeProto(r)
	if err != nil {
		return nil, err
	}
	info := func(v any) error {
		if len(r.Info) == 0 {
			return nil
		}
		return json.Unmarshal(r.Info, v)
	}
	var state appStateInfo
	switch r.Kind {
	case "message":
		var mi types.MessageInfo
		err = info(&mi)
		raw, _ := payload.(*waE2E.Message)
		return (&events.Message{Info: mi, RawMessage: raw}).UnwrapRaw(), err // as whatsmeow dispatches it
	case "undecryptable":
		e := &events.UndecryptableMessage{}
		return e, info(e)
	case "history_sync":
		hs, _ := payload.(*waHistorySync.HistorySync)
		return &events.HistorySync{Data: hs}, nil
	case "group_info":
		e := &events.GroupInfo{}
		return e, info(e)
	case "joined_group":
		e := &events.JoinedGroup{}
		return e, info(e)
	case "archive":
		a, _ := payload.(*waSyncAction.ArchiveChatAction)
		err = info(&state)
		return &events.Archive{JID: state.JID, Timestamp: state.Timestamp, Action: a, FromFullSync: state.FromFullSync}, err
	case "mute":
		a, _ := payload.(*waSyncAction.MuteAction)
		err = info(&state)
		return &events.Mute{JID: state.JID, Timestamp: state.Timestamp, Action: a, FromFullSync: state.FromFullSync}, err
	case "pin":
		a, _ := payload.(*waSyncAction.PinAction)
		err = info(&state)
		return &events.Pin{JID: state.JID, Timestamp: state.Timestamp, Action: a, FromFullSync: state.FromFullSync}, err
	case "media_retry":
		e := &events.MediaRetry{}
		return e, info(e)
	}
	return nil, fmt.Errorf("unsupported recorded kind %q", r.Kind)
}

// decodeProto returns the recorded protobuf payload, or nil when the file has none.
func decodeProto(r recorded) (proto.Message, error) {
	if r.Proto == nil {
		return nil, nil
	}
	newMsg, ok := protoKinds[r.Kind]
	if !ok {
		return nil, fmt.Errorf("kind %q has no protobuf payload", r.Kind)
	}
	raw, err := base64.StdEncoding.DecodeString(*r.Proto)
	if err != nil {
		return nil, err
	}
	msg := newMsg()
	return msg, proto.UnmarshalOptions{AllowPartial: true}.Unmarshal(raw, msg)
}

func Replay(ctx context.Context, p *Pipeline, items []any) error {
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.Handle(item)
	}
	return nil
}

// DumpRaw is the sorted SQL dump of the raw tables, without receive times, for golden comparison.
func DumpRaw(st *store.Store) (string, error) { return st.DumpRaw(context.Background()) }
