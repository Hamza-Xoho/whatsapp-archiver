package wa

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// The client's history parser reads the account's own JID at parse time, so it works after pairing too.
func TestClientHistoryParserUsesOwnJID(t *testing.T) {
	device := &wmstore.Device{}
	c := &Client{client: whatsmeow.NewClient(device, nil)}
	parse := c.HistoryParser()
	own := types.NewADJID("111", 0, 5)
	device.ID = &own
	web := &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{ID: proto.String("A"), FromMe: proto.Bool(true)}, Message: &waE2E.Message{Conversation: proto.String("hi")}}
	evt, err := parse(types.NewJID("4915111", types.DefaultUserServer), web)
	if err != nil || evt.Info.Sender.String() != "111@s.whatsapp.net" || evt.Message.GetConversation() != "hi" {
		t.Fatalf("evt=%+v err=%v", evt, err)
	}
}

func TestSessionDSNEnablesForeignKeys(t *testing.T) {
	dsn := sessionDSN(filepath.Join(t.TempDir(), "session.db"))
	if !strings.Contains(dsn, "_pragma=foreign_keys(1)") {
		t.Fatalf("dsn=%s", dsn)
	}
	c, err := sqlstore.New(t.Context(), "sqlite", dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
}

// whatsmeow decrypts inside a read-then-write transaction. Two such transactions on the session
// pool must queue behind each other, not fail at once with SQLITE_BUSY.
func TestSessionDSNReadThenWriteTxnsWaitInsteadOfBusy(t *testing.T) {
	db, err := sql.Open("sqlite", sessionDSN(filepath.Join(t.TempDir(), "session.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatal(err)
	}

	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen1 int
	if err := tx1.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&seen1); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		errc <- func() error {
			tx2, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx2.Rollback()
			var seen2 int
			if err := tx2.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&seen2); err != nil {
				return err
			}
			if _, err := tx2.ExecContext(ctx, "INSERT INTO t (x) VALUES (2)"); err != nil {
				return err
			}
			return tx2.Commit()
		}()
	}()

	time.Sleep(200 * time.Millisecond)
	if _, err := tx1.ExecContext(ctx, "INSERT INTO t (x) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("second read-then-write transaction failed instead of waiting: %v", err)
	}
}

func TestDecideVersion(t *testing.T) {
	tests := []struct {
		name, built, latest, override string
		latestErr                     error
		check                         bool
		want, reason                  string
	}{
		{"override", "2.3000.2", "2.3000.3", "2.3000.9", nil, true, "2.3000.9", "override"},
		{"latest", "2.3000.2", "2.3000.3", "", nil, true, "2.3000.3", "latest"},
		{"fetch error", "2.3000.2", "", "", errors.New("offline"), true, "2.3000.2", "offline"},
		{"older", "2.3000.3", "2.3000.2", "", nil, true, "2.3000.3", "built-in"},
		{"off", "2.3000.2", "2.3000.3", "", nil, false, "2.3000.2", "built-in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := decideVersion(tt.built, tt.latest, tt.override, tt.latestErr, tt.check)
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got=%s reason=%s", got, reason)
			}
		})
	}
}

func TestHistoryPropsUseConfiguredLimits(t *testing.T) {
	p := historyProps(3650, 4096)
	if !p.GetRequireFullSync() || p.GetHistorySyncConfig().GetFullSyncDaysLimit() != 3650 || p.GetHistorySyncConfig().GetFullSyncSizeMbLimit() != 4096 {
		t.Fatalf("props=%+v", p)
	}
}
func TestClassifyDownloadErr(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{whatsmeow.ErrMediaDownloadFailedWith403, 403},
		{whatsmeow.ErrMediaDownloadFailedWith404, 404},
		{whatsmeow.ErrMediaDownloadFailedWith410, 410},
	} {
		got := classifyDownloadErr(tt.err)
		if !errors.Is(got, ErrMediaExpired) {
			t.Fatalf("err=%v", tt.err)
		}
		reason, class, status := DescribeDownloadError(got)
		if class != "expired" || status != tt.status || !strings.Contains(reason, fmt.Sprint(tt.status)) {
			t.Fatalf("DescribeDownloadError(%v) = %q, %q, %d", tt.err, reason, class, status)
		}
	}
	other := errors.New("network")
	if !errors.Is(classifyDownloadErr(other), other) {
		t.Fatal("other error lost")
	}
}

// B2: the adapter hands whatsmeow the handler's verdict and still prints the pairing line and forwards control events.
func TestEventHandlerPassesVerdictAndKeepsSideEffects(t *testing.T) {
	ctl := make(chan any, 2)
	var out bytes.Buffer
	for _, want := range []bool{true, false} {
		h := eventHandler(func(any) bool { return want }, true, &out, ctl)
		if got := h(&events.PairSuccess{}); got != want {
			t.Fatalf("verdict=%v, want %v", got, want)
		}
	}
	if !strings.Contains(out.String(), "Paired. Capture is running.") {
		t.Fatalf("pairing output=%q", out.String())
	}
	eventHandler(func(any) bool { return false }, false, io.Discard, ctl)(&events.Connected{})
	if len(ctl) != 1 {
		t.Fatalf("control events forwarded=%d, want 1", len(ctl))
	}
}
