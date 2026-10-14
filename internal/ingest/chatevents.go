package ingest

import (
	"encoding/json"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

// groupChange is the stored Value of a group event: only the fields that changed are present.
type groupChange struct {
	Notify    string   `json:"notify,omitempty"`
	Sender    string   `json:"sender,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Type      string   `json:"type,omitempty"`
	Name      *string  `json:"name,omitempty"`
	Topic     *string  `json:"topic,omitempty"`
	Announce  *bool    `json:"announce,omitempty"`
	Locked    *bool    `json:"locked,omitempty"`
	Ephemeral *uint32  `json:"ephemeral,omitempty"` // disappearing timer in seconds, 0 = off
	Join      []string `json:"join,omitempty"`
	Leave     []string `json:"leave,omitempty"`
	Promote   []string `json:"promote,omitempty"`
	Demote    []string `json:"demote,omitempty"`
}

// stateChange is the stored Value of an archive, mute or pin app-state event: the chat's state after it.
type stateChange struct {
	Action       string `json:"action"`
	Archived     *bool  `json:"archived,omitempty"`
	Muted        *bool  `json:"muted,omitempty"`
	MuteEnd      *int64 `json:"mute_end,omitempty"`
	Pinned       *bool  `json:"pinned,omitempty"`
	FromFullSync bool   `json:"from_full_sync"`
}

// FromChatEvent normalises a group or chat app-state event. A GroupInfo that changes participants (join, leave,
// promote, demote) is KindParticipants, even when it also changes other fields; its Value keeps all of them.
func FromChatEvent(evt any, received time.Time) (Result, bool) {
	var chat types.JID
	var sender *types.JID
	var ts time.Time
	var payload any
	var raw []byte
	kind := KindAppState
	switch e := evt.(type) {
	case *events.GroupInfo:
		chat, sender, ts, kind = e.JID, e.Sender, e.Timestamp, KindGroupInfo
		c := groupChange{Notify: e.Notify, Join: jids(e.Join), Leave: jids(e.Leave), Promote: jids(e.Promote), Demote: jids(e.Demote)}
		if e.Name != nil {
			c.Name = &e.Name.Name
		}
		if e.Topic != nil {
			c.Topic = &e.Topic.Topic
		}
		if e.Announce != nil {
			c.Announce = &e.Announce.IsAnnounce
		}
		if e.Locked != nil {
			c.Locked = &e.Locked.IsLocked
		}
		if e.Ephemeral != nil {
			c.Ephemeral = &e.Ephemeral.DisappearingTimer
		}
		if len(c.Join)+len(c.Leave)+len(c.Promote)+len(c.Demote) > 0 {
			kind = KindParticipants
		}
		c.Sender = jidString(sender)
		payload = c
	case *events.JoinedGroup: // carries no WhatsApp timestamp
		chat, sender, kind = e.JID, e.Sender, KindGroupInfo
		payload = groupChange{Notify: e.Notify, Sender: jidString(sender), Reason: e.Reason, Type: e.Type, Name: &e.Name}
	case *events.Archive:
		chat, ts = e.JID, e.Timestamp
		payload = stateChange{Action: "archive", Archived: e.Action.Archived, FromFullSync: e.FromFullSync}
		raw, _ = proto.Marshal(e.Action)
	case *events.Mute:
		chat, ts = e.JID, e.Timestamp
		payload = stateChange{Action: "mute", Muted: e.Action.Muted, MuteEnd: e.Action.MuteEndTimestamp, FromFullSync: e.FromFullSync}
		raw, _ = proto.Marshal(e.Action)
	case *events.Pin:
		chat, ts = e.JID, e.Timestamp
		payload = stateChange{Action: "pin", Pinned: e.Action.Pinned, FromFullSync: e.FromFullSync}
		raw, _ = proto.Marshal(e.Action)
	default:
		return Result{}, false
	}
	value, _ := json.Marshal(payload) // plain strings, bools and numbers: cannot fail
	var unix int64
	if !ts.IsZero() {
		unix = ts.Unix()
	}
	c := chat.String()
	ce := store.ChatEvent{ChatJID: c, Kind: string(kind), EventID: stableID(c, string(kind), string(value), strconv.FormatInt(unix, 10)), SenderJID: jidString(sender), Timestamp: unix, ReceivedAt: received.Unix(), Value: string(value), RawProto: raw}
	return Result{Kind: kind, Chat: store.Chat{JID: c}, ChatEvent: &ce, Received: received}, true
}

func jids(list []types.JID) []string {
	out := make([]string, 0, len(list))
	for _, j := range list {
		out = append(out, j.String())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
func jidString(j *types.JID) string {
	if j == nil {
		return ""
	}
	return j.String()
}
