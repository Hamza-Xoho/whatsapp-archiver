package store

import (
	"context"
	"errors"
)

// ContactName is the address-book name of a JID, as the contact store reports it.
type ContactName struct{ JID, Name string }

// SetContactNames is not implemented yet (red stub).
func (s *Store) SetContactNames(ctx context.Context, names []ContactName) (int, error) {
	return 0, errors.New("not implemented")
}
