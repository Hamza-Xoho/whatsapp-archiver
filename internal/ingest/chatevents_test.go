package ingest_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
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

// I1: two group changes that differ only in the join-approval mode (same sender, same second) are two rows.
func TestApprovalModeChangesAreDistinct(t *testing.T) {
	p, st := chatEventPipeline(t)
	for _, required := range []bool{true, false} {
		p.Handle(&events.GroupInfo{JID: jid("g@g.us"), Sender: jidPtr("5@s.whatsapp.net"), Timestamp: time.Unix(500, 0),
			MembershipApprovalMode: &types.GroupMembershipApprovalMode{IsJoinApprovalRequired: required}})
	}
	want := []string{
		`{"sender":"5@s.whatsapp.net","membership_approval":false}`,
		`{"sender":"5@s.whatsapp.net","membership_approval":true}`,
	}
	if got := rows(t, st, `SELECT value FROM chat_events ORDER BY value`); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}

// I1: a deleted group is stored with the reason.
func TestGroupDeleteStoredWithReason(t *testing.T) {
	p, st := chatEventPipeline(t)
	p.Handle(&events.GroupInfo{JID: jid("g@g.us"), Sender: jidPtr("5@s.whatsapp.net"), Timestamp: time.Unix(500, 0), Delete: &types.GroupDelete{Deleted: true, DeleteReason: "admin"}})
	want := []string{`group_info|5@s.whatsapp.net|500|{"sender":"5@s.whatsapp.net","delete":{"Deleted":true,"DeleteReason":"admin"}}|`}
	if got := rows(t, st, chatEventCols); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}

// I1: every kind of change a GroupInfo can carry reaches the stored value.
func TestGroupInfoKeepsEveryChangeKind(t *testing.T) {
	link := &types.GroupLinkChange{Type: types.GroupLinkChangeTypeSub, Group: types.GroupLinkTarget{JID: jid("sub@g.us"), GroupName: types.GroupName{Name: "Sub"}}}
	invite := "https://chat.whatsapp.com/abc"
	evt := &events.GroupInfo{JID: jid("g@g.us"), Timestamp: time.Unix(500, 0), Delete: &types.GroupDelete{Deleted: true},
		Link: link, Unlink: link, NewInviteLink: &invite, MembershipApprovalMode: &types.GroupMembershipApprovalMode{IsJoinApprovalRequired: true},
		Suspended: true, Unsuspended: true, JoinReason: "invite", ParticipantVersionID: "v7",
		UnknownChanges: []*waBinary.Node{{Tag: "mystery", Attrs: waBinary.Attrs{"b": "2", "a": "1"}}}}
	r, _ := ingest.FromChatEvent(evt, time.Unix(1000, 0))
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.ChatEvent.Value), &got); err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(got))
	want := []string{"delete", "invite_link", "join_reason", "link", "membership_approval", "suspended", "unknown", "unlink", "unsuspended", "version"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys %v, want %v (value %s)", keys, want, r.ChatEvent.Value)
	}
	if string(got["unknown"]) != `["\u003cmystery a=\"1\" b=\"2\"/\u003e"]` || string(got["invite_link"]) != `"https://chat.whatsapp.com/abc"` || string(got["version"]) != `"v7"` {
		t.Fatalf("value %s", r.ChatEvent.Value)
	}
	b, _ := ingest.FromChatEvent(evt, time.Unix(2000, 0))
	if b.ChatEvent.Value != r.ChatEvent.Value || b.ChatEvent.EventID != r.ChatEvent.EventID {
		t.Fatalf("not deterministic: %s vs %s", r.ChatEvent.Value, b.ChatEvent.Value)
	}
}

// I2: being added to the same group again (new participant version) is a new row; the create key is kept.
func TestRepeatJoinedGroupIsDistinct(t *testing.T) {
	p, st := chatEventPipeline(t)
	for _, version := range []string{"v1", "v2"} {
		p.Handle(&events.JoinedGroup{Reason: "invite", CreateKey: "CK", GroupInfo: types.GroupInfo{JID: jid("g@g.us"), GroupName: types.GroupName{Name: "Club"}, ParticipantVersionID: version, GroupCreated: time.Unix(400, 0)}})
	}
	want := []string{
		`{"reason":"invite","name":"Club","version":"v1","create_key":"CK","created":400}`,
		`{"reason":"invite","name":"Club","version":"v2","create_key":"CK","created":400}`,
	}
	if got := rows(t, st, `SELECT value FROM chat_events ORDER BY value`); !slices.Equal(got, want) {
		t.Fatalf("chat_events:\n got %q\nwant %q", got, want)
	}
}

// M2: archive, unarchive, archive within one second are three events; ts stays in seconds.
func TestArchiveToggleWithinOneSecondIsThreeRows(t *testing.T) {
	p, st := chatEventPipeline(t)
	for i, v := range []bool{true, false, true} {
		p.Handle(&events.Archive{JID: jid("g@g.us"), Timestamp: time.Unix(600, int64(i+1)*250*int64(time.Millisecond)), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(v)}})
	}
	if got := rows(t, st, `SELECT ts,value FROM chat_events ORDER BY rowid`); len(got) != 3 || got[0] != got[2] || got[0][:4] != "600|" {
		t.Fatalf("chat_events=%q", got)
	}
}
