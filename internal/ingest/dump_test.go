package ingest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// dumpOf fills a fresh store with rows in reverse sort order, all received at `received`, and dumps it.
func dumpOf(t *testing.T, received int64) string {
	t.Helper()
	st := testkit.Store(t)
	ctx := context.Background()
	at := time.Unix(received, 0)
	for _, id := range []string{"b", "a"} {
		m := testkit.Message("c@g.us", id, time.Unix(10, 0))
		m.ReceivedAt, m.RawProto = received, []byte{0xab, 0x01}
		if _, err := st.InsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	steps := []error{
		st.UpsertChat(ctx, store.Chat{JID: "c@g.us", Name: "it's"}),
		st.UpsertParticipant(ctx, store.Participant{JID: "sender@lid", PushName: "S"}),
		st.InsertHistoryChunk(ctx, store.HistoryChunk{ID: "h", SyncType: "RECENT", ReceivedAt: received}),
	}
	_, err := st.InsertIDMapping(ctx, store.IDMapping{LID: "2@lid", PN: "1@s.whatsapp.net", Source: "history", FirstSeen: at})
	steps = append(steps, err)
	_, err = st.InsertEvent(ctx, store.MessageEvent{ChatJID: "c@g.us", TargetID: "a", Kind: "reaction", EventID: "e", Value: "+", ReceivedAt: received, RawProto: []byte{1}})
	steps = append(steps, err)
	_, err = st.InsertChatEvent(ctx, store.ChatEvent{ChatJID: "c@g.us", Kind: "app_state", EventID: "e", Value: "{}", ReceivedAt: received, RawProto: []byte{}})
	steps = append(steps, err)
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}
	dump, err := ingest.DumpRaw(st)
	if err != nil {
		t.Fatal(err)
	}
	return dump
}

// H14 (e): DumpRaw lists every raw table as sorted INSERT lines, blobs as hex, without receive times.
func TestDumpRawIsSortedAndOmitsReceiveTimes(t *testing.T) {
	early, late := dumpOf(t, 100), dumpOf(t, 200)
	if early != late {
		t.Fatalf("the dump depends on receive time:\n%s\n---\n%s", early, late)
	}
	want := strings.Join([]string{
		`INSERT INTO chats(jid,name,type,parent_jid) VALUES('c@g.us','it''s','','');`,
		`INSERT INTO participants(jid,push_name,contact_name) VALUES('sender@lid','S','');`,
		`INSERT INTO id_mappings(lid,pn,source) VALUES('2@lid','1@s.whatsapp.net','history');`,
		`INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('c@g.us','a','sender@lid',0,'','text','a','','','','[]',0,0,0,0,'{}','live',10,X'AB01');`,
		`INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('c@g.us','b','sender@lid',0,'','text','b','','','','[]',0,0,0,0,'{}','live',10,X'AB01');`,
		`INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,new_text,value,raw_proto) VALUES('c@g.us','a','reaction','e','','','','live',0,'','+',X'01');`,
		`INSERT INTO chat_events(chat_jid,kind,event_id,sender_jid,ts,value,raw_proto) VALUES('c@g.us','app_state','e','',0,'{}',X'');`,
		`INSERT INTO history_sync_chunks(id,stage,spool_id,sync_type,chunk_order,progress,conversation_count,message_count,oldest_ts,newest_ts) VALUES('h','imported',0,'RECENT',0,0,0,0,0,0);`,
	}, "\n") + "\n"
	if early != want {
		t.Fatalf("dump:\n%s\nwant:\n%s", early, want)
	}
}
