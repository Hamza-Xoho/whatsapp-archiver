package ingest_test

import (
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func jidPtr(value string) *types.JID {
	j := jid(value)
	return &j
}
func chatEventPipeline(t *testing.T) (*ingest.Pipeline, *store.Store) {
	st := testkit.Store(t)
	return ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()), st
}

const chatEventCols = `SELECT kind,sender_jid,ts,value,hex(raw_proto) FROM chat_events ORDER BY ts`

// H7 / Table 1: group, archive, mute and pin events keep their payload, sender, WhatsApp timestamp and raw action.
func TestChatEventsStored(t *testing.T) {
	p, st := chatEventPipeline(t)
	g := jid("g@g.us")
	p.Handle(&events.GroupInfo{JID: g, Sender: jidPtr("5@s.whatsapp.net"), Timestamp: time.Unix(500, 0),
		Name: &types.GroupName{Name: "New Name"}, Topic: &types.GroupTopic{Topic: "About"}, Announce: &types.GroupAnnounce{IsAnnounce: true},
		Locked: &types.GroupLocked{IsLocked: false}, Ephemeral: &types.GroupEphemeral{IsEphemeral: true, DisappearingTimer: 86400}})
	p.Handle(&events.GroupInfo{JID: g, Sender: jidPtr("5@s.whatsapp.net"), Timestamp: time.Unix(501, 0),
		Join: []types.JID{jid("6@s.whatsapp.net")}, Leave: []types.JID{jid("7@lid")}, Promote: []types.JID{jid("8@lid")}, Demote: []types.JID{jid("9@lid")}})
	p.Handle(&events.JoinedGroup{Reason: "invite", Type: "new", Sender: jidPtr("5@s.whatsapp.net"), GroupInfo: types.GroupInfo{JID: g, GroupName: types.GroupName{Name: "Club"}}})
	p.Handle(&events.Archive{JID: g, Timestamp: time.Unix(600, 0), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}})
	p.Handle(&events.Mute{JID: g, Timestamp: time.Unix(601, 0), Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(9999)}, FromFullSync: true})
	p.Handle(&events.Pin{JID: g, Timestamp: time.Unix(602, 0), Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}})
	want := []string{
		`group_info|5@s.whatsapp.net|0|{"sender":"5@s.whatsapp.net","reason":"invite","type":"new","name":"Club"}|`,
		`group_info|5@s.whatsapp.net|500|{"sender":"5@s.whatsapp.net","name":"New Name","topic":"About","announce":true,"locked":false,"ephemeral":86400}|`,
		`participants|5@s.whatsapp.net|501|{"sender":"5@s.whatsapp.net","join":["6@s.whatsapp.net"],"leave":["7@lid"],"promote":["8@lid"],"demote":["9@lid"]}|`,
		`app_state||600|{"action":"archive","archived":true,"from_full_sync":false}|0801`,
		`app_state||601|{"action":"mute","muted":true,"mute_end":9999,"from_full_sync":true}|0801108F4E`,
		`app_state||602|{"action":"pin","pinned":true,"from_full_sync":false}|0801`,
	}
	if got := rows(t, st, chatEventCols); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}

// H7: the event ID comes from stable fields, so a redelivered group change is one row; a later change is another.
func TestSameGroupInfoTwiceIsOneRow(t *testing.T) {
	mk := func(ts int64) *events.GroupInfo {
		return &events.GroupInfo{JID: jid("g@g.us"), Timestamp: time.Unix(ts, 0), Name: &types.GroupName{Name: "New Name"}, Join: []types.JID{jid("5@s.whatsapp.net")}}
	}
	a, _ := ingest.FromChatEvent(mk(500), time.Unix(1000, 0))
	b, _ := ingest.FromChatEvent(mk(500), time.Unix(2000, 0))
	if a.ChatEvent.EventID != b.ChatEvent.EventID {
		t.Fatalf("same event, different IDs: %s vs %s", a.ChatEvent.EventID, b.ChatEvent.EventID)
	}
	p, st := chatEventPipeline(t)
	p.Handle(mk(500))
	p.Handle(mk(500))
	if got := rows(t, st, `SELECT count(*) FROM chat_events`); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("after redelivery rows=%v", got)
	}
	p.Handle(mk(501))
	if got := rows(t, st, `SELECT ts FROM chat_events ORDER BY ts`); !slices.Equal(got, []string{"500", "501"}) {
		t.Fatalf("after a later change rows=%v", got)
	}
}

// H7: archiving and unarchiving at the same moment are two different events with different values.
func TestArchiveOnAndOffAreDistinct(t *testing.T) {
	arch := func(v bool) *events.Archive {
		return &events.Archive{JID: jid("g@g.us"), Timestamp: time.Unix(600, 0), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(v)}}
	}
	on, _ := ingest.FromChatEvent(arch(true), time.Unix(1000, 0))
	off, _ := ingest.FromChatEvent(arch(false), time.Unix(1000, 0))
	if on.ChatEvent.EventID == off.ChatEvent.EventID {
		t.Fatalf("archive on and off share ID %s", on.ChatEvent.EventID)
	}
	p, st := chatEventPipeline(t)
	p.Handle(arch(true))
	p.Handle(arch(false))
	want := []string{
		`app_state||600|{"action":"archive","archived":false,"from_full_sync":false}|0800`,
		`app_state||600|{"action":"archive","archived":true,"from_full_sync":false}|0801`,
	}
	if got := rows(t, st, `SELECT kind,sender_jid,ts,value,hex(raw_proto) FROM chat_events ORDER BY value`); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}

// H7: whatsmeow dispatches app-state events with the mutation's action, which may be missing; the event is still stored.
func TestAppStateEventWithoutActionIsStored(t *testing.T) {
	p, st := chatEventPipeline(t)
	p.Handle(&events.Archive{JID: jid("g@g.us"), Timestamp: time.Unix(600, 0)})
	p.Handle(&events.Mute{JID: jid("g@g.us"), Timestamp: time.Unix(601, 0)})
	p.Handle(&events.Pin{JID: jid("g@g.us"), Timestamp: time.Unix(602, 0)})
	want := []string{
		`app_state||600|{"action":"archive","from_full_sync":false}|`,
		`app_state||601|{"action":"mute","from_full_sync":false}|`,
		`app_state||602|{"action":"pin","from_full_sync":false}|`,
	}
	if got := rows(t, st, chatEventCols); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}
