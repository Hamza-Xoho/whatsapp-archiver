package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// History ledger stages (R10). A chunk is ledgered as received when its blob is spooled and as imported when its
// messages are stored; both are inserts, so history_sync_chunks stays insert-only.
const (
	StageReceived = "received"
	StageImported = "imported"
)

// HistoryChunk is a row of history_sync_chunks, keyed by (ID, Stage). An empty Stage means imported, which is what the
// importer writes today (one row after the whole chunk is stored).
type HistoryChunk struct {
	ID, Stage, SyncType                                   string
	SpoolID                                               int64
	ChunkOrder, Progress, ConversationCount, MessageCount int
	Oldest, Newest, ReceivedAt                            int64
}

const insertChunkSQL = `INSERT INTO history_sync_chunks(id,stage,spool_id,sync_type,chunk_order,progress,conversation_count,message_count,oldest_ts,newest_ts,received_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`

func (h HistoryChunk) args() []any {
	if h.Stage == "" {
		h.Stage = StageImported
	}
	return []any{h.ID, h.Stage, h.SpoolID, h.SyncType, h.ChunkOrder, h.Progress, h.ConversationCount, h.MessageCount, h.Oldest, h.Newest, h.ReceivedAt}
}

func (s *Store) InsertHistoryChunk(ctx context.Context, h HistoryChunk) error {
	_, err := s.insert(ctx, insertChunkSQL, h.args()...)
	return err
}

// Blob formats of history_spool.blob_format.
const (
	BlobZlib = "zlib"
	BlobRaw  = "raw"
)

// SpoolMetaDenylist lists the JSON keys SpoolReceive refuses in Meta (RED STUB: empty).
func SpoolMetaDenylist() []string { return nil }

// Spool is a row of history_spool: a downloaded, decrypted, still compressed history blob and the non-key fields of
// its notification (R9). The importer works through the rows with ImportedAt zero.
type Spool struct {
	ID                                  int64
	ChunkID, SyncType, Meta, BlobFormat string // BlobFormat is "zlib" or "raw"; Meta is a JSON object
	ChunkOrder, Progress, Size          int
	ReceivedAt, ImportedAt              int64
	Attempts                            int
	Error                               string
	Blob                                []byte
}

// SpoolReceive spools a chunk and ledgers it as received in one transaction (ADR-37: the notification is acked only
// after this commits). A resent chunk is a no-op: it reports the existing row and isNew false. Size is taken from the
// blob.
func (s *Store) SpoolReceive(ctx context.Context, sp Spool, chunk HistoryChunk) (id int64, isNew bool, err error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin spool: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if sp.Meta == "" {
		sp.Meta = "{}"
	}
	if sp.Blob == nil {
		sp.Blob = []byte{}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO history_spool(chunk_id,sync_type,chunk_order,progress,meta,size,received_at,blob_format,blob) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(chunk_id) DO NOTHING`,
		sp.ChunkID, sp.SyncType, sp.ChunkOrder, sp.Progress, sp.Meta, len(sp.Blob), sp.ReceivedAt, sp.BlobFormat, sp.Blob)
	if err != nil {
		return 0, false, fmt.Errorf("spool chunk %s: %w", sp.ChunkID, classify(err))
	}
	isNew, id, err = inserted(result)
	if err != nil {
		return 0, false, err
	}
	if !isNew {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM history_spool WHERE chunk_id=?`, sp.ChunkID).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("read spooled chunk %s: %w", sp.ChunkID, err)
		}
		return id, false, nil // nothing changed; the transaction is rolled back
	}
	if chunk.ID == "" {
		chunk.ID = sp.ChunkID
	}
	chunk.Stage, chunk.SpoolID = StageReceived, id
	if _, err = tx.ExecContext(ctx, insertChunkSQL, chunk.args()...); err != nil {
		return 0, false, fmt.Errorf("ledger chunk %s: %w", chunk.ID, classify(err))
	}
	if err = tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit spool: %w", classify(err))
	}
	return id, true, nil
}

// SpoolTodo lists up to limit spool rows that are not imported yet, oldest first, blobs included.
func (s *Store) SpoolTodo(ctx context.Context, limit int) ([]Spool, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id,chunk_id,sync_type,chunk_order,progress,meta,size,received_at,attempts,error,blob_format,blob
 FROM history_spool WHERE imported_at IS NULL ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read spool: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Spool
	for rows.Next() {
		var sp Spool
		if err = rows.Scan(&sp.ID, &sp.ChunkID, &sp.SyncType, &sp.ChunkOrder, &sp.Progress, &sp.Meta, &sp.Size, &sp.ReceivedAt, &sp.Attempts, &sp.Error, &sp.BlobFormat, &sp.Blob); err != nil {
			return nil, fmt.Errorf("scan spool: %w", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SpoolImported marks a spool row imported at the given time and ledgers the chunk as imported, in one transaction. An
// empty chunk.ID is the spool row's chunk.
func (s *Store) SpoolImported(ctx context.Context, id, at int64, chunk HistoryChunk) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin spool import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var chunkID string
	if err = tx.QueryRowContext(ctx, `SELECT chunk_id FROM history_spool WHERE id=?`, id).Scan(&chunkID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no spool row %d: %w", id, err)
		}
		return fmt.Errorf("read spool row %d: %w", id, err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE history_spool SET imported_at=?,error='' WHERE id=?`, at, id); err != nil {
		return fmt.Errorf("mark spool row %d imported: %w", id, classify(err))
	}
	if chunk.ID == "" {
		chunk.ID = chunkID
	}
	chunk.Stage, chunk.SpoolID = StageImported, id
	if _, err = tx.ExecContext(ctx, insertChunkSQL, chunk.args()...); err != nil {
		return fmt.Errorf("ledger chunk %s: %w", chunk.ID, classify(err))
	}
	return tx.Commit()
}

// SpoolFailed counts a failed import attempt and keeps the error; the row stays on the todo list.
func (s *Store) SpoolFailed(ctx context.Context, id int64, errText string) error {
	_, err := s.write.ExecContext(ctx, `UPDATE history_spool SET attempts=attempts+1,error=? WHERE id=?`, errText, id)
	return err
}

// SpoolClearBlobs empties the blob of every row imported before the given unix time (decision Q1: seven days after the
// import). The row, its size and its metadata stay. It returns the number of rows cleared.
func (s *Store) SpoolClearBlobs(ctx context.Context, importedBefore int64) (int64, error) {
	result, err := s.write.ExecContext(ctx, `UPDATE history_spool SET blob=X'' WHERE imported_at IS NOT NULL AND imported_at<? AND length(blob)>0`, importedBefore)
	if err != nil {
		return 0, fmt.Errorf("clear spool blobs: %w", err)
	}
	return result.RowsAffected()
}
