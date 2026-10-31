package web

import (
	"context"
	"io"
	"os"
)

// streamSpooledExport decouples the SQLite read snapshot from client
// backpressure. Artifact generation retains page-bounded memory in a private
// disk spool; the snapshot is released before the first response-body write.
func (s *Server) streamSpooledExport(ctx context.Context, w io.Writer, scope exportScope) error {
	spool, err := os.CreateTemp("", "whatsapp-archiver-export-*")
	if err != nil {
		_ = scope.snapshot.Close()
		return err
	}
	name := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(name)
	}()
	switch scope.format {
	case "json":
		err = s.streamJSON(ctx, spool, scope)
	case "html":
		err = s.streamHTML(ctx, spool, scope)
	case "txt":
		err = s.streamText(ctx, spool, scope)
	default:
		err = errExportFailed
	}
	closeErr := scope.snapshot.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		_, err = spool.Seek(0, io.SeekStart)
	}
	if err == nil {
		err = copyContext(ctx, w, spool)
	}
	return err
}
