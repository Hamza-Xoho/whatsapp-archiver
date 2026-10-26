package store

import (
	"context"
	"errors"
)

// Scaffold for the W2(d) red commit: the API exists, the behaviour does not.

// Fold is the FR-10 folder.
func Fold(s string) string { return s }

// ErrInUse is returned by OpenExclusive when another connection has the archive open.
var ErrInUse = errors.New("archive is in use")

// OpenExclusive opens the archive for a maintenance command.
func OpenExclusive(path string) (*Store, error) { return nil, errors.New("not implemented") }

// RebuildDerived recreates every derived table from the raw tables.
func (s *Store) RebuildDerived(ctx context.Context) error { return errors.New("not implemented") }
