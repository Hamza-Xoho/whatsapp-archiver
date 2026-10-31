package web

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"whatsapp-archiver/internal/store"
)

func archiveMediaName(chat, id, filename string) string {
	base := safeMediaName(filename, id)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	sum := sha256.Sum256([]byte(chat + "\x00" + id))
	return stem + "_" + hex.EncodeToString(sum[:]) + ext
}

// exportArchiveName is deterministic per record and stores no archive-sized state. The Store batches the exact
// collision count into each record; unique safe basenames remain familiar and every other name uses a full hash.
func exportArchiveName(x exportScope, r store.ExportRecord) string {
	m := r.Media
	if m == nil {
		return ""
	}
	base := safeMediaName(m.Filename, r.Message.ID)
	originalBase := filepath.Base(strings.ReplaceAll(m.Filename, "\\", "/"))
	if r.MediaNameCount != 1 || base != originalBase || base == "_chat.txt" || base == "missing-media.txt" {
		return archiveMediaName(x.jid, r.Message.ID, base)
	}
	return base
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func copyContext(ctx context.Context, w io.Writer, r io.Reader) error {
	_, err := io.CopyBuffer(w, contextReader{ctx, r}, make([]byte, 32<<10))
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (s *Server) streamZIP(ctx context.Context, w io.Writer, x exportScope) error {
	z := zip.NewWriter(w)
	manifest, err := z.Create("missing-media.txt")
	if err != nil {
		return err
	}
	err = s.eachExportMedia(ctx, x, func(r store.ExportRecord) error {
		m := r.Media
		if m == nil {
			return nil
		}
		name := exportArchiveName(x, r)
		path, ok := s.insideMediaDir(m.Path)
		state := m.State
		if !ok || state != "done" {
			if state == "done" {
				state = "missing"
			}
			_, e := fmt.Fprintf(manifest, "%s\t%s\t%s\n", r.Message.ID, name, state)
			return e
		}
		if st, e2 := os.Stat(path); e2 != nil || !st.Mode().IsRegular() {
			_, e := fmt.Fprintf(manifest, "%s\t%s\tmissing\n", r.Message.ID, name)
			return e
		}
		return nil
	})
	if err != nil {
		return err
	}
	tw, err := z.CreateHeader(&zip.FileHeader{Name: "_chat.txt", Method: zip.Store})
	if err != nil {
		return err
	}
	textBuffer := bufio.NewWriterSize(tw, 64<<10)
	if err = s.streamText(ctx, textBuffer, x); err != nil {
		return err
	}
	if err = textBuffer.Flush(); err != nil {
		return err
	}
	err = s.eachExportMedia(ctx, x, func(r store.ExportRecord) error {
		m := r.Media
		if m == nil || m.State != "done" {
			return nil
		}
		path, ok := s.insideMediaDir(m.Path)
		if !ok {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return nil
		}
		name := exportArchiveName(x, r)
		zw, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e == nil {
			e = copyContext(ctx, zw, f)
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		return e
	})
	if err != nil {
		return err
	}
	return z.Close()
}

func (s *Server) eachExportMedia(ctx context.Context, x exportScope, fn func(store.ExportRecord) error) error {
	var ts int64
	var id string
	hasCursor := false
	for {
		page, err := s.d.Store.ExportMediaPage(ctx, store.ExportParams{CanonicalJID: x.jid, Snapshot: x.snapshot, From: x.from, To: x.to, AfterTS: ts, AfterID: id, HasCursor: hasCursor, Limit: exportPageSize})
		if err != nil {
			return err
		}
		for _, record := range page {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = fn(record); err != nil {
				return err
			}
			ts, id, hasCursor = record.Message.Timestamp, record.Message.ID, true
		}
		if len(page) < exportPageSize {
			return nil
		}
	}
}
