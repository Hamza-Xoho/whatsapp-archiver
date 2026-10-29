package store

import "fmt"

// OptimizeForDevSeed trades crash durability for seed speed. It is only for a disposable synthetic archive that can
// be reproduced from its numeric seed; normal capture must never call it. Closing the Store checkpoints the WAL.
func (s *Store) OptimizeForDevSeed() error {
	if _, err := s.write.Exec(`PRAGMA synchronous=OFF; PRAGMA wal_autocheckpoint=10000`); err != nil {
		return fmt.Errorf("configure synthetic archive: %w", err)
	}
	return nil
}
