package wa

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
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
	for _, err := range []error{whatsmeow.ErrMediaDownloadFailedWith404, whatsmeow.ErrMediaDownloadFailedWith410} {
		if !errors.Is(classifyDownloadErr(err), ErrMediaExpired) {
			t.Fatalf("err=%v", err)
		}
	}
	other := errors.New("network")
	if !errors.Is(classifyDownloadErr(other), other) {
		t.Fatal("other error lost")
	}
}
