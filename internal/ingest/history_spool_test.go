package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

type failImportedStore struct{ *store.Store }

func (s *failImportedStore) SpoolImported(context.Context, int64, int64, store.HistoryChunk) error {
	return errors.New("injected imported-marker failure")
}

type countingTodoStore struct {
	*store.Store
	todo chan struct{}
}

func (s *countingTodoStore) SpoolTodo(ctx context.Context, limit int) ([]store.Spool, error) {
	s.todo <- struct{}{}
	return s.Store.SpoolTodo(ctx, limit)
}

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
	if first[0].BlobFormat != store.BlobZlib {
		t.Fatalf("spooled blob format=%q, want the stripped chunk to remain zlib-compressed", first[0].BlobFormat)
	}
	strippedAgain, err := ingest.StripHistorySecrets(first[0].Blob, first[0].BlobFormat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(strippedAgain, first[0].Blob) {
		t.Fatal("spooled zlib blob still contained key material")
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

func TestHistorySpoolPublishesSuccessOnlyAfterImportedMarkerCommits(t *testing.T) {
	st := testkit.Store(t)
	bus := activity.NewBus(10)
	var logs bytes.Buffer
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()).WithHistoryParser(historyParser()).WithActivity(bus)
	spool := ingest.NewHistorySpool(&failImportedStore{Store: st}, p, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := spool.Receive(t.Context(), spoolNotification(), threeMessageChunk().Data); err != nil {
		t.Fatal(err)
	}
	if imported, err := spool.ImportNext(t.Context()); err == nil || imported {
		t.Fatalf("import=(%v,%v), want marker failure", imported, err)
	}
	for _, event := range bus.Recent() {
		if event.Kind == "history_chunk" {
			t.Fatalf("published successful history event before imported marker committed: %+v", event)
		}
	}
	if strings.Contains(logs.String(), "history chunk imported") {
		t.Fatalf("logged successful import before imported marker committed: %s", logs.String())
	}
}

func TestHistorySpoolPublishesSuccessAfterImportedMarkerCommits(t *testing.T) {
	st := testkit.Store(t)
	bus := activity.NewBus(10)
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, log).WithHistoryParser(historyParser()).WithActivity(bus)
	spool := ingest.NewHistorySpool(st, p, log)
	if err := spool.Receive(t.Context(), spoolNotification(), threeMessageChunk().Data); err != nil {
		t.Fatal(err)
	}
	if imported, err := spool.ImportNext(t.Context()); err != nil || !imported {
		t.Fatalf("import=(%v,%v), want committed success", imported, err)
	}
	events := bus.Recent()
	if len(events) != 1 || events[0].Kind != "history_chunk" || !strings.Contains(events[0].Detail, "3 messages") {
		t.Fatalf("activity=%+v, want one successful history chunk", events)
	}
	if !strings.Contains(logs.String(), "history chunk imported") {
		t.Fatalf("success log missing after marker commit: %s", logs.String())
	}
	if got := rows(t, st, "SELECT imported_at IS NOT NULL FROM history_spool"); strings.Join(got, ",") != "1" {
		t.Fatalf("imported marker=%v, want committed", got)
	}
}

func TestDuplicateHistorySpoolReceiveDoesNotWakeIdleImporter(t *testing.T) {
	st := testkit.Store(t)
	counting := &countingTodoStore{Store: st, todo: make(chan struct{}, 8)}
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	spool := ingest.NewHistorySpool(counting, p, quiet())
	n, hs := spoolNotification(), threeMessageChunk().Data
	if err := spool.Receive(t.Context(), n, hs); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); spool.Run(ctx) }()
	// One poll imports the row, one observes the empty queue, and one follows the original buffered wake. The importer
	// is then blocked. A duplicate must not create a fourth poll.
	for range 3 {
		select {
		case <-counting.todo:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatal("importer did not become idle")
		}
	}
	if err := spool.Receive(t.Context(), n, hs); err != nil {
		t.Fatal(err)
	}
	select {
	case <-counting.todo:
		t.Fatal("duplicate spool row woke the idle importer and caused an extra store poll")
	case <-time.After(250 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("importer did not stop")
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

// Moving history import out of whatsmeow's synchronous handler must not move it outside the event panic boundary.
// One bad history item is retried from the durable spool, counted, and cannot take down capture.
func TestHistorySpoolRecoversImporterPanicAndContinues(t *testing.T) {
	st := testkit.Store(t)
	health := &healthFake{}
	writer := &panicWriter{Store: st, panics: 1}
	p := ingest.NewPipeline(writer, ingest.NewFilter(nil, nil), &queueFake{}, health, quiet()).WithHistoryParser(historyParser())
	spool := ingest.NewHistorySpool(writer, p, quiet())
	if err := spool.Receive(t.Context(), spoolNotification(), threeMessageChunk().Data); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); spool.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := rows(t, st, "SELECT imported_at IS NOT NULL FROM history_spool"); strings.Join(got, ",") == "1" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("history importer did not stop")
	}
	if got := rows(t, st, "SELECT imported_at IS NOT NULL FROM history_spool"); strings.Join(got, ",") != "1" {
		t.Fatalf("spool row was not retried after panic: %v", got)
	}
	if health.panics != 1 {
		t.Fatalf("recovered panics=%d, want 1", health.panics)
	}
	if got := rows(t, st, "SELECT count(*) FROM messages"); strings.Join(got, ",") != "3" {
		t.Fatalf("messages=%v, want the retried chunk imported once", got)
	}
}
