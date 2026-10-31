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
	"strconv"
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
func ordinalName(base string, n int) string {
	if n <= 1 {
		return base
	}
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)] + "_" + strconv.Itoa(n) + ext
}

// exportArchiveName is deterministic per record and stores no archive-sized state. Familiar basenames are retained
// when unambiguous; reserved or adversarial collisions use the full canonical-chat/message-id hash.
func (s *Server) exportArchiveName(ctx context.Context, x exportScope, r store.ExportRecord) (string, error) {
	m := r.Media
	if m == nil {
		return "", nil
	}
	base := safeMediaName(m.Filename, r.Message.ID)
	ordinal, _, err := s.d.Store.ExportMediaNameFacts(ctx, x.jid, m.Filename, base, r.Message.ID, m.MessageTimestamp)
	if err != nil {
		return "", err
	}
	candidate := ordinalName(base, ordinal)
	_, collision, err := s.d.Store.ExportMediaNameFacts(ctx, x.jid, m.Filename, candidate, r.Message.ID, m.MessageTimestamp)
	if err != nil {
		return "", err
	}
	originalBase := filepath.Base(strings.ReplaceAll(m.Filename, "\\", "/"))
	if collision || base != originalBase || candidate == "_chat.txt" || candidate == "missing-media.txt" {
		candidate = archiveMediaName(x.jid, r.Message.ID, base)
	}
	return candidate, nil
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
		name, e := s.exportArchiveName(ctx, x, r)
		if e != nil {
			return e
		}
		path, ok := s.insideMediaDir(m.Path)
		state := m.State
		if !ok || state != "done" {
			if state == "done" {
				state = "missing"
			}
			_, e = fmt.Fprintf(manifest, "%s\t%s\t%s\n", r.Message.ID, name, state)
			return e
		}
		if st, e2 := os.Stat(path); e2 != nil || !st.Mode().IsRegular() {
			_, e = fmt.Fprintf(manifest, "%s\t%s\tmissing\n", r.Message.ID, name)
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
		name, e := s.exportArchiveName(ctx, x, r)
		if e != nil {
			_ = f.Close()
			return e
		}
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
		page, err := s.d.Store.ExportMediaPage(ctx, store.ExportParams{CanonicalJID: x.jid, From: x.from, To: x.to, AfterTS: ts, AfterID: id, HasCursor: hasCursor, Limit: exportPageSize})
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
