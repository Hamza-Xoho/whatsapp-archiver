package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func spoolNotification() *waE2E.HistorySyncNotification {
	return &waE2E.HistorySyncNotification{
		SyncType:                     waE2E.HistorySyncType_FULL.Enum(),
		ChunkOrder:                   proto.Uint32(3),
		Progress:                     proto.Uint32(40),
		OriginalMessageID:            proto.String("notification-1"),
		OldestMsgInChunkTimestampSec: proto.Int64(123),
		FileLength:                   proto.Uint64(456),
		DirectPath:                   proto.String("/v/t62/history"),
		PeerDataRequestSessionID:     proto.String("request-1"),
		MediaKey:                     []byte("notification media key must not be stored"),
		FileSHA256:                   []byte("file hash must not be stored"),
		FileEncSHA256:                []byte("encrypted hash must not be stored"),
		EncHandle:                    proto.String("encrypted handle must not be stored"),
	}
}

func secretChunk(secret string) *waHistorySync.HistorySync {
	hs := threeMessageChunk().Data
	hs.SyncType = waHistorySync.HistorySync_FULL.Enum()
	hs.ChunkOrder = proto.Uint32(3)
	hs.Progress = proto.Uint32(40)
	hs.Conversations[0].Messages[0].Message.MessageSecret = []byte(secret)
	return hs
}

func TestHistorySpoolStripsSecretsUsesAllowlistAndHashesTheCleanChunk(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	spool := ingest.NewHistorySpool(st, p, quiet())
	if err := spool.Receive(t.Context(), spoolNotification(), secretChunk("first secret")); err != nil {
		t.Fatal(err)
	}
	first, err := st.SpoolTodo(t.Context(), 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("todo=%+v err=%v", first, err)
	}
	var meta map[string]any
	if err = json.Unmarshal([]byte(first[0].Meta), &meta); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"chunk_order", "direct_path", "file_length", "oldest_msg_timestamp", "original_message_id", "peer_data_request_session_id", "progress", "sync_type"}
	for _, key := range wantKeys {
		if _, ok := meta[key]; !ok {
			t.Errorf("allowlist meta %s lacks %q", first[0].Meta, key)
		}
	}
	if len(meta) != len(wantKeys) {
		t.Fatalf("allowlist meta keys=%v, want exactly %v", meta, wantKeys)
	}
	if strings.Contains(string(first[0].Blob), "first secret") || strings.Contains(first[0].Meta, "must not be stored") {
		t.Fatalf("spooled row retains key material: meta=%s blob=%q", first[0].Meta, first[0].Blob)
	}

	// A resend whose only difference is stripped key material is the same chunk. The ID must therefore be computed
	// from the stripped chunk, not from the downloaded input.
	if err = spool.Receive(t.Context(), spoolNotification(), secretChunk("different secret")); err != nil {
		t.Fatal(err)
	}
	after, err := st.SpoolTodo(t.Context(), 10)
	if err != nil || len(after) != 1 || after[0].ChunkID != first[0].ChunkID {
		t.Fatalf("resend todo=%+v err=%v, want the same single chunk %q", after, err, first[0].ChunkID)
	}
	if got := rows(t, st, "SELECT stage||':'||count(*) FROM history_sync_chunks GROUP BY stage"); strings.Join(got, ",") != "received:1" {
		t.Fatalf("ledger=%v, want one received row", got)
	}
}

// ADR-37 crash proof: the received row is durable before import. If the first importer dies after storing one
// message, the next process resumes the same row; insert-only message keys make the replay idempotent.
func TestHistorySpoolResumesAfterCrashWithoutDuplicates(t *testing.T) {
	st := testkit.Store(t)
	path := st.Path()
	serve, cancelServe := context.WithCancel(t.Context())
	cancelServe()
	cut := newPipeline(&cutWriter{Store: st, from: 2}, &queueFake{}, &healthFake{}, newFakeTime()).WithContext(serve)
	spool := ingest.NewHistorySpool(st, cut, quiet())
	if err := spool.Receive(t.Context(), spoolNotification(), threeMessageChunk().Data); err != nil {
		t.Fatal(err)
	}
	if imported, err := spool.ImportNext(t.Context()); err == nil || imported {
		t.Fatalf("first import=(%v,%v), want a failed pending row", imported, err)
	}
	if got := rows(t, st, "SELECT count(*),attempts,imported_at IS NULL FROM history_spool"); strings.Join(got, ",") != "1|1|1" {
		t.Fatalf("spool after crash=%v", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	again := ingest.NewHistorySpool(reopened, historyPipeline(reopened, ingest.NewFilter(nil, nil), &queueFake{}), quiet())
	if imported, err := again.ImportNext(t.Context()); err != nil || !imported {
		t.Fatalf("restart import=(%v,%v), want imported", imported, err)
	}
	if imported, err := again.ImportNext(t.Context()); err != nil || imported {
		t.Fatalf("empty import=(%v,%v), want no pending row", imported, err)
	}
	if got := rows(t, reopened, "SELECT count(*),count(DISTINCT id) FROM messages"); strings.Join(got, ",") != "3|3" {
		t.Fatalf("messages=%v, want three unique rows", got)
	}
	if got := rows(t, reopened, "SELECT stage||':'||count(*) FROM history_sync_chunks GROUP BY stage ORDER BY stage"); strings.Join(got, ",") != "imported:1,received:1" {
		t.Fatalf("ledger=%v, want one row at each stage", got)
	}
}

func fiftyThousandChunk() *waHistorySync.HistorySync {
	items := make([]*waHistorySync.HistorySyncMsg, 50_000)
	for i := range items {
		items[i] = &waHistorySync.HistorySyncMsg{Message: webMsg(fmt.Sprintf("H-%05d", i), "3@lid", false, text("history"))}
	}
	return &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_FULL.Enum(), ChunkOrder: proto.Uint32(1), Progress: proto.Uint32(50), Conversations: []*waHistorySync.Conversation{{ID: proto.String("g@g.us"), Messages: items}}}
}

// A spool import runs outside the whatsmeow event handler. Its many short writer transactions may interleave with a
// live Pipeline.Ingest call; the live event must not sit behind the whole 50k-message chunk.
func TestLiveMessageDuringFiftyThousandMessageImportIsStoredUnderOneSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("50k history latency regression")
	}
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	spool := ingest.NewHistorySpool(st, p, quiet())
	if err := spool.Receive(t.Context(), spoolNotification(), fiftyThousandChunk()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); spool.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := rows(t, st, "SELECT count(*) FROM messages"); len(got) == 1 && got[0] != "0" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	live := &events.Message{Message: &waE2E.Message{Conversation: proto.String("live")}}
	live.Info.Chat = jid("g@g.us")
	live.Info.Sender = jid("3@lid")
	live.Info.ID = "LIVE-DURING-IMPORT"
	live.Info.Timestamp = time.Unix(20, 0)
	started := time.Now()
	if err := p.Ingest(t.Context(), ingest.FromMessage(live, "live", time.Now())); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed >= time.Second {
		t.Fatalf("live message stored in %s during history import, want under 1s", elapsed)
	}
	if got := rows(t, st, "SELECT count(*) FROM messages WHERE id='LIVE-DURING-IMPORT'"); strings.Join(got, ",") != "1" {
		t.Fatalf("live row count=%v, want 1", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("history importer did not stop on context cancellation")
	}
}

// Keep the imported raw blob parser pinned to the same permissive protobuf behavior as the live history path.
func TestHistorySpoolAcceptsUnknownProtobufFields(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	spool := ingest.NewHistorySpool(st, p, quiet())
	hs := secretChunk("secret")
	hs.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if err := spool.Receive(t.Context(), spoolNotification(), hs); err != nil {
		t.Fatal(err)
	}
	if imported, err := spool.ImportNext(t.Context()); err != nil || !imported {
		t.Fatalf("import=(%v,%v)", imported, err)
	}
	if got := rows(t, st, "SELECT count(*) FROM messages"); strings.Join(got, ",") != "3" {
		t.Fatalf("messages=%v", got)
	}
}
