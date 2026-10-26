package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"whatsapp-archiver/internal/store"
)

// One writer: a maintenance command opens the archive exclusively and is refused while the service (or anything else)
// has it open.
func TestOpenExclusiveIsRefusedWhileTheArchiveIsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	service, err := store.Open(path)
	must(t, err)
	apply(t, service, mb("c@g.us", "A", "text", "x", 1))
	if x, err := store.OpenExclusive(path); !errors.Is(err, store.ErrInUse) {
		if x != nil {
			_ = x.Close()
		}
		t.Fatalf("OpenExclusive next to an open store: err = %v, want ErrInUse", err)
	}
	must(t, service.Close())

	x, err := store.OpenExclusive(path)
	must(t, err)
	// While it is held nobody else gets in: a second exclusive open is refused too.
	if y, err := store.OpenExclusive(path); !errors.Is(err, store.ErrInUse) {
		if y != nil {
			_ = y.Close()
		}
		t.Fatalf("second OpenExclusive: err = %v, want ErrInUse", err)
	}
	must(t, x.RebuildDerived(context.Background()))
	must(t, x.Close())
	again, err := store.OpenExclusive(path)
	must(t, err)
	must(t, again.Close())
}

func TestOpenExclusiveDoesNotCreateAnArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "archive.db")
	if _, err := store.OpenExclusive(path); err == nil {
		t.Fatal("OpenExclusive on a missing archive must fail")
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Fatal("OpenExclusive created the data folder")
	}
}
