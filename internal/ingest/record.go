package ingest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"sync"
)

type Recorder struct {
	dir string
	mu  sync.Mutex
	seq int
}
type recorded struct {
	Kind      string `json:"kind"`
	Chat      string `json:"chat"`
	Sender    string `json:"sender"`
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	FromMe    bool   `json:"from_me"`
	Proto     string `json:"proto"`
}

func NewRecorder(dir string) *Recorder { return &Recorder{dir: dir} }
func (r *Recorder) Wrap(next func(any)) func(any) {
	return func(evt any) {
		if item, ok := encodeEvent(evt); ok {
			r.mu.Lock()
			r.seq++
			_ = os.MkdirAll(r.dir, 0o700)
			data, _ := json.MarshalIndent(item, "", "  ")
			_ = os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("%06d-%s.json", r.seq, item.Kind)), data, 0o600)
			r.mu.Unlock()
		}
		next(evt)
	}
}
func encodeEvent(evt any) (recorded, bool) {
	switch e := evt.(type) {
	case *events.Message:
		raw, _ := proto.Marshal(e.RawMessage)
		if e.RawMessage == nil {
			raw, _ = proto.Marshal(e.Message)
		}
		return recorded{Kind: "message", Chat: e.Info.Chat.String(), Sender: e.Info.Sender.String(), ID: string(e.Info.ID), Timestamp: e.Info.Timestamp.Unix(), FromMe: e.Info.IsFromMe, Proto: base64.StdEncoding.EncodeToString(raw)}, true
	default:
		return recorded{}, false
	}
}
func decodeEvent(r recorded) (any, error) {
	if r.Kind != "message" {
		return nil, fmt.Errorf("unsupported recorded kind %s", r.Kind)
	}
	raw, err := base64.StdEncoding.DecodeString(r.Proto)
	if err != nil {
		return nil, err
	}
	var msg waE2E.Message
	if err = proto.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	chat, err := types.ParseJID(r.Chat)
	if err != nil {
		return nil, err
	}
	sender, err := types.ParseJID(r.Sender)
	if err != nil {
		return nil, err
	}
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: r.FromMe}, ID: types.MessageID(r.ID)}, Message: &msg, RawMessage: &msg}, nil
}
