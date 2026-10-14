package ingest_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
)

// fullInfo sets every MessageInfo field Handle or a later reader could use, with UTC times so values compare equal.
func fullInfo(id string) types.MessageInfo {
	return types.MessageInfo{
		MessageSource: types.MessageSource{Chat: jid("10000000100@g.us"), Sender: jid("20000000001:12@lid"), IsFromMe: true, IsGroup: true,
			AddressingMode: types.AddressingModeLID, SenderAlt: jid("10000000001:12@s.whatsapp.net"), RecipientAlt: jid("10000000002@s.whatsapp.net"),
			BroadcastListOwner: jid("10000000003@s.whatsapp.net")},
		ID: types.MessageID(id), ServerID: 77, Type: "text", PushName: "Alice", Timestamp: time.Unix(1_700_000_123, 0).UTC(), Category: "peer",
		Multicast: true, MediaType: "image", Edit: types.EditAttributeMessageEdit,
		MsgBotInfo:     types.MsgBotInfo{EditType: types.EditTypeLast, EditTargetID: "T", EditSenderTimestampMS: time.UnixMilli(1_700_000_000_123).UTC(), ClientThreadID: "thread"},
		MsgMetaInfo:    types.MsgMetaInfo{TargetID: "T2", TargetSender: jid("10000000004@s.whatsapp.net"), ThreadMessageID: "T3", DeprecatedLIDSession: proto.Bool(true)},
		DeviceSentMeta: &types.DeviceSentMeta{DestinationJID: "10000000002@s.whatsapp.net", Phash: "2:abc"},
	}
}

// everyKind returns one Go-constructed event of each supported kind, with every field set that the type has.
func everyKind() []any {
	ts := time.Unix(1_700_000_500, 0).UTC()
	g := jid("10000000100@g.us")
	inner := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("wrapped"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q")}}}
	msg := (&events.Message{Info: fullInfo("M1"), RawMessage: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: inner}}}).UnwrapRaw()
	hs := &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(), ChunkOrder: proto.Uint32(2), Progress: proto.Uint32(40),
		PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{{PnJID: proto.String("10000000001@s.whatsapp.net"), LidJID: proto.String("20000000001@lid")}},
		Conversations: []*waHistorySync.Conversation{{ID: proto.String("20000000001@lid"), Name: proto.String("Alice"), Messages: []*waHistorySync.HistorySyncMsg{{
			Message: &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{RemoteJID: proto.String("20000000001@lid"), ID: proto.String("H1")}, MessageTimestamp: proto.Uint64(99), Message: text("old")}}}}}}
	gi := &events.GroupInfo{JID: g, Notify: "subject", Sender: jidPtr("20000000001@lid"), SenderPN: jidPtr("10000000001@s.whatsapp.net"), Timestamp: ts,
		Name:   &types.GroupName{Name: "Renamed", NameSetAt: ts, NameSetBy: jid("20000000001@lid"), NameSetByPN: jid("10000000001@s.whatsapp.net")},
		Topic:  &types.GroupTopic{Topic: "About", TopicID: "TID", TopicSetAt: ts, TopicSetBy: jid("20000000001@lid"), TopicDeleted: true},
		Locked: &types.GroupLocked{IsLocked: true}, Announce: &types.GroupAnnounce{IsAnnounce: true, AnnounceVersionID: "AV"},
		Ephemeral: &types.GroupEphemeral{IsEphemeral: true, DisappearingTimer: 86400}, MembershipApprovalMode: &types.GroupMembershipApprovalMode{IsJoinApprovalRequired: true},
		Delete: &types.GroupDelete{Deleted: true, DeleteReason: "admin"},
		Link:   &types.GroupLinkChange{Type: types.GroupLinkChangeTypeSub, Group: types.GroupLinkTarget{JID: jid("10000000101@g.us"), GroupName: types.GroupName{Name: "Sub"}}},
		Unlink: &types.GroupLinkChange{Type: types.GroupLinkChangeTypeParent, UnlinkReason: "delete"}, NewInviteLink: proto.String("https://chat.whatsapp.com/x"),
		PrevParticipantVersionID: "P1", ParticipantVersionID: "P2", JoinReason: "invite",
		Join: []types.JID{jid("10000000005@s.whatsapp.net")}, Leave: []types.JID{jid("20000000006@lid")}, Promote: []types.JID{jid("20000000007@lid")}, Demote: []types.JID{jid("20000000008@lid")},
		Suspended: true, Unsuspended: true, UnknownChanges: []*waBinary.Node{{Tag: "future", Attrs: waBinary.Attrs{"kind": "new"}, Content: []byte("x")}}}
	jg := &events.JoinedGroup{Reason: "invite", Type: "new", CreateKey: "CK", Sender: jidPtr("20000000001@lid"), SenderPN: jidPtr("10000000001@s.whatsapp.net"), Notify: "create",
		GroupInfo: types.GroupInfo{JID: g, OwnerJID: jid("20000000001@lid"), OwnerPN: jid("10000000001@s.whatsapp.net"),
			GroupName: types.GroupName{Name: "Club", NameSetAt: ts}, GroupTopic: types.GroupTopic{Topic: "T"}, GroupLocked: types.GroupLocked{IsLocked: true},
			GroupAnnounce: types.GroupAnnounce{IsAnnounce: true}, GroupEphemeral: types.GroupEphemeral{IsEphemeral: true, DisappearingTimer: 60},
			GroupIncognito: types.GroupIncognito{IsIncognito: true}, GroupParent: types.GroupParent{IsParent: true, DefaultMembershipApprovalMode: "request_required"},
			GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: jid("10000000102@g.us")}, GroupIsDefaultSub: types.GroupIsDefaultSub{IsDefaultSubGroup: true},
			GroupMembershipApprovalMode: types.GroupMembershipApprovalMode{IsJoinApprovalRequired: true}, AddressingMode: types.AddressingModeLID,
			GroupCreated: ts, CreatorCountryCode: "49", ParticipantVersionID: "PV", ParticipantCount: 2, MemberAddMode: types.GroupMemberAddModeAdmin, Suspended: true,
			Participants: []types.GroupParticipant{{JID: jid("20000000001@lid"), PhoneNumber: jid("10000000001@s.whatsapp.net"), LID: jid("20000000001@lid"), IsAdmin: true, IsSuperAdmin: true,
				DisplayName: "A", Error: 403, AddRequest: &types.GroupParticipantAddRequest{Code: "c", Expiration: ts}}}}}
	return []any{
		msg,
		&events.UndecryptableMessage{Info: fullInfo("U1"), IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce, DecryptFailMode: events.DecryptFailHide},
		&events.HistorySync{Data: hs},
		gi,
		jg,
		&events.Archive{JID: g, Timestamp: ts, Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}, FromFullSync: true},
		&events.Archive{JID: g, Timestamp: ts}, // a dispatched action may be missing
		&events.Mute{JID: g, Timestamp: ts, Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(9999)}},
		&events.Pin{JID: g, Timestamp: ts, Action: &waSyncAction.PinAction{Pinned: proto.Bool(false)}, FromFullSync: true},
		&events.MediaRetry{Ciphertext: []byte{1, 2}, IV: []byte{3}, Timestamp: ts, MessageID: "M9", ChatID: g, SenderID: jid("20000000001@lid"), FromMe: true},
		&events.MediaRetry{Error: &events.MediaRetryError{Code: 2}, Timestamp: ts, MessageID: "M8", ChatID: jid("20000000001@lid")},
	}
}

// sameEvent compares a loaded event with the original: protobufs with proto.Equal, everything else with reflect.DeepEqual.
func sameEvent(t *testing.T, i int, want, got any) {
	t.Helper()
	protos := func(a, b proto.Message) bool { return proto.Equal(a, b) }
	ok := false
	switch w := want.(type) {
	case *events.Message:
		g, isType := got.(*events.Message)
		ok = isType && protos(w.RawMessage, g.RawMessage) && protos(w.Message, g.Message) && reflect.DeepEqual(w.Info, g.Info) && w.IsEphemeral == g.IsEphemeral
	case *events.HistorySync:
		g, isType := got.(*events.HistorySync)
		ok = isType && protos(w.Data, g.Data)
	case *events.Archive:
		g, isType := got.(*events.Archive)
		ok = isType && protos(w.Action, g.Action) && (w.Action == nil) == (g.Action == nil) && w.JID == g.JID && w.Timestamp.Equal(g.Timestamp) && w.FromFullSync == g.FromFullSync
	case *events.Mute:
		g, isType := got.(*events.Mute)
		ok = isType && protos(w.Action, g.Action) && w.JID == g.JID && w.Timestamp.Equal(g.Timestamp) && w.FromFullSync == g.FromFullSync
	case *events.Pin:
		g, isType := got.(*events.Pin)
		ok = isType && protos(w.Action, g.Action) && w.JID == g.JID && w.Timestamp.Equal(g.Timestamp) && w.FromFullSync == g.FromFullSync
	default:
		ok = reflect.DeepEqual(want, got)
	}
	if !ok {
		t.Errorf("event %d (%T) changed by record/load:\nwant %+v\n got %+v", i, want, want, got)
	}
}

// H14 (c, d): every supported kind is recorded, the handler gets the same event, and Load rebuilds it exactly.
func TestRecordLoadRoundTripForEachKind(t *testing.T) {
	dir := t.TempDir()
	evts := everyKind()
	var passed []any
	wrapped := ingest.NewRecorder(dir).Wrap(func(v any) { passed = append(passed, v) })
	for _, e := range evts {
		wrapped(e)
	}
	wrapped(&events.Connected{}) // not a supported kind: passed on, not recorded
	if len(passed) != len(evts)+1 {
		t.Fatalf("handler got %d events, want %d", len(passed), len(evts)+1)
	}
	for i, e := range evts {
		if passed[i] != e {
			t.Fatalf("handler got a different event at %d", i)
		}
	}
	entries, _ := os.ReadDir(dir)
	wantKinds := []string{"message", "undecryptable", "history_sync", "group_info", "joined_group", "archive", "archive", "mute", "pin", "media_retry", "media_retry"}
	if len(entries) != len(wantKinds) {
		t.Fatalf("recorded %d files, want %d", len(entries), len(wantKinds))
	}
	for i, e := range entries {
		if !strings.HasSuffix(e.Name(), "-"+wantKinds[i]+".json") {
			t.Errorf("file %d = %s, want kind %s", i, e.Name(), wantKinds[i])
		}
	}
	loaded, err := ingest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(evts) {
		t.Fatalf("loaded %d events, want %d", len(loaded), len(evts))
	}
	for i := range evts {
		sameEvent(t, i, evts[i], loaded[i])
	}
}

// A second recorder on the same folder (pair, then run, both with --record) continues the sequence.
func TestRecorderContinuesAfterExistingFiles(t *testing.T) {
	dir := t.TempDir()
	ingest.NewRecorder(dir).Wrap(func(any) {})(messageEvent("1@lid", "A", text("first")))
	ingest.NewRecorder(dir).Wrap(func(any) {})(messageEvent("1@lid", "B", text("second")))
	loaded, err := ingest.Load(dir)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("loaded %d events, err %v; want both recordings", len(loaded), err)
	}
	if loaded[0].(*events.Message).Info.ID != "A" || loaded[1].(*events.Message).Info.ID != "B" {
		t.Fatalf("order: %s, %s", loaded[0].(*events.Message).Info.ID, loaded[1].(*events.Message).Info.ID)
	}
}

// A recorder that cannot write logs the error and still passes the event on unchanged.
func TestRecorderWriteFailureIsLoggedAndEventPassedOn(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	evt := messageEvent("1@lid", "A", text("x"))
	var got any
	ingest.NewRecorder(filepath.Join(blocked, "rec")).WithLogger(slog.New(slog.NewTextHandler(&logs, nil))).Wrap(func(v any) { got = v })(evt)
	if got != evt {
		t.Fatal("event not passed on")
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "not recorded") {
		t.Fatalf("logs = %q, want an error", logs.String())
	}
}
