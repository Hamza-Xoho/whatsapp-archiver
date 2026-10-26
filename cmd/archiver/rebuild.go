package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

// rebuildDerived is `archiver rebuild-derived --data <dir>`: it recreates chat_canonical, chat_stats and the search
// index from the raw tables in one transaction (schema 4.3). It takes the archive exclusively, so it refuses while the
// service has it open: exactly one connection writes archive.db.
func rebuildDerived(cfg *config.Config, out io.Writer) error {
	st, err := store.OpenExclusive(filepath.Join(cfg.DataDir, "archive.db"))
	if errors.Is(err, store.ErrInUse) {
		return errors.New("the archive is open in another process: stop the service (archiver run) and try again")
	}
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	start := time.Now()
	if err = st.RebuildDerived(context.Background()); err != nil {
		return fmt.Errorf("rebuild derived tables: %w", err)
	}
	_, err = fmt.Fprintf(out, "rebuilt chat_canonical, chat_stats and the search index in %s\n", time.Since(start).Round(time.Millisecond))
	return err
}
