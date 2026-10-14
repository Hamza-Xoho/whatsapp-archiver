package wa

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
)

// IsPaired reports whether dataDir's session.db holds a paired device, without network access.
// A missing session.db is unpaired and is not created.
func IsPaired(ctx context.Context, dataDir string) (bool, error) {
	path := filepath.Join(dataDir, "session.db")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("check session: %w", err)
	}
	container, err := sqlstore.New(ctx, "sqlite", sessionDSN(path), nil)
	if err != nil {
		return false, fmt.Errorf("open session: %w", err)
	}
	defer func() { _ = container.Close() }()
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return false, fmt.Errorf("load device: %w", err)
	}
	return device.ID != nil, nil
}

// pairLine maps pairing events and QR channel items to the line shown to the user ("" for none).
func pairLine(v any) string {
	switch e := v.(type) {
	case *events.PairSuccess:
		return "Paired. Capture is running."
	case *events.PairError:
		return fmt.Sprintf("Pairing failed: %v", e.Error)
	case whatsmeow.QRChannelItem:
		switch {
		case e.Event == whatsmeow.QRChannelTimeout.Event:
			return "QR code timed out; run `archiver pair` again."
		case e.Event == whatsmeow.QRChannelEventError:
			return fmt.Sprintf("Pairing failed: %v", e.Error)
		case strings.HasPrefix(e.Event, "err-"):
			return "Pairing failed: " + e.Event
		}
	}
	return ""
}
