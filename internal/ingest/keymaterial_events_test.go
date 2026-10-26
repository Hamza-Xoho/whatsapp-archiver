package ingest_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

// N1 (fix round 2): key-material protocol messages (history notifications, app-state key shares, ...) have no target, and
// their payload no longer reaches the raw proto. A burst of them in the self chat must still be one event each: the
// event_id derives from the message's own ID, like a reaction's, so it is the same on the live and the history path.

func notificationMsg(n byte) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
		HistorySyncNotification: &waE2E.HistorySyncNotification{MediaKey: []byte{n, n, n}, DirectPath: proto.String("/blob/" + string(rune('a'+n))), ChunkOrder: proto.Uint32(uint32(n))}},
		MessageContextInfo: &waE2E.MessageContextInfo{DeviceListMetadataVersion: proto.Int32(2)}}
}

func keyShareMsg(n byte) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum(),
		AppStateSyncKeyShare: &waE2E.AppStateSyncKeyShare{Keys: []*waE2E.AppStateSyncKey{{KeyData: &waE2E.AppStateSyncKeyData{KeyData: []byte{n, n, n, n}}}}}},
		MessageContextInfo: &waE2E.MessageContextInfo{DeviceListMetadataVersion: proto.Int32(2)}}
}

func TestKeyMaterialProtocolEventsAreOneEventPerMessageID(t *testing.T) {
	for name, build := range map[string]func(byte) *waE2E.Message{"history notification": notificationMsg, "app state key share": keyShareMsg} {
		t.Run(name, func(t *testing.T) {
			first := ingest.FromMessage(messageEvent("me@s.whatsapp.net", "N1", build(1)), "live", time.Unix(20, 0))
			second := ingest.FromMessage(messageEvent("me@s.whatsapp.net", "N2", build(2)), "live", time.Unix(21, 0))
			if first.Event == nil || second.Event == nil || first.Event.EventID == second.Event.EventID {
				t.Fatalf("event ids %v and %v; want two distinct ids", first.Event, second.Event)
			}
			again := ingest.FromMessage(messageEvent("me@s.whatsapp.net", "N1", build(1)), "history", time.Unix(99, 0))
			if again.Event.EventID != first.Event.EventID {
				t.Fatalf("the same message ID gives %q on a second sighting, want %q", again.Event.EventID, first.Event.EventID)
			}
			if len(first.Event.RawProto) != 0 {
				t.Fatalf("raw_proto of a key-material event = %d bytes, want none", len(first.Event.RawProto))
			}
		})
	}
}

func TestKeyMaterialProtocolEventsAreAllStoredWithTheirSourceID(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	p.Handle(messageEvent("me@s.whatsapp.net", "N1", notificationMsg(1)))
	p.Handle(messageEvent("me@s.whatsapp.net", "N2", notificationMsg(2)))
	p.Handle(messageEvent("me@s.whatsapp.net", "K1", keyShareMsg(1)))
	p.Handle(messageEvent("me@s.whatsapp.net", "K2", keyShareMsg(2)))
	got := rows(t, st, "SELECT source_id,value FROM message_events ORDER BY source_id")
	want := []string{"K1|APP_STATE_SYNC_KEY_SHARE", "K2|APP_STATE_SYNC_KEY_SHARE", "N1|HISTORY_SYNC_NOTIFICATION", "N2|HISTORY_SYNC_NOTIFICATION"}
	if len(got) != len(want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stored %v, want %v", got, want)
		}
	}
}

// R11: a gap whose target is the second notification's own ID closes on that notification (the …6F66 case: a protocol
// event with no target, own ID stored as source_id).
func TestGapClosesOnTheSecondOfTwoKeyMaterialNotifications(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	p.Handle(&events.UndecryptableMessage{Info: messageEvent("me@s.whatsapp.net", "N2", text("x")).Info})
	if n, _ := st.OpenGapCount(t.Context()); n != 1 {
		t.Fatalf("gaps = %d before the notifications, want 1", n)
	}
	p.Handle(messageEvent("me@s.whatsapp.net", "N1", notificationMsg(1)))
	p.Handle(messageEvent("me@s.whatsapp.net", "N2", notificationMsg(2)))
	if n, _ := st.OpenGapCount(t.Context()); n != 0 {
		t.Fatalf("gaps = %d after the second notification arrived, want 0", n)
	}
}
