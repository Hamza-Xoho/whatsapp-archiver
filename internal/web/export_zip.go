package web

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	_, err := io.CopyBuffer(w, contextReader{ctx, r}, make([]byte, 1<<20))
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (s *Server) streamZIP(ctx context.Context, w io.Writer, x exportScope) error {
	plan, err := s.prepareZIP(ctx, x)
	closeErr := x.snapshot.Close()
	if err != nil {
		return err
	}
	defer plan.close()
	if closeErr != nil {
		return closeErr
	}
	return s.writePreparedZIP(ctx, w, plan)
}

type preparedZIP struct {
	transcript *os.File
	media      *os.File
}

type preparedZIPMedia struct {
	ID, Name, State, Path string
}

func (s *Server) prepareZIP(ctx context.Context, x exportScope) (*preparedZIP, error) {
	transcript, err := os.CreateTemp("", "whatsapp-archiver-transcript-*")
	if err != nil {
		return nil, err
	}
	media, err := os.CreateTemp("", "whatsapp-archiver-media-plan-*")
	if err != nil {
		_ = transcript.Close()
		_ = os.Remove(transcript.Name())
		return nil, err
	}
	plan := &preparedZIP{transcript: transcript, media: media}
	ok := false
	defer func() {
		if !ok {
			plan.close()
		}
	}()
	if err = s.streamText(ctx, transcript, x); err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(media)
	err = s.eachExportMedia(ctx, x, func(r store.ExportRecord) error {
		m := r.Media
		if m == nil {
			return nil
		}
		return encoder.Encode(preparedZIPMedia{ID: r.Message.ID, Name: exportArchiveName(x, r), State: m.State, Path: m.Path})
	})
	if err != nil {
		return nil, err
	}
	if _, err = transcript.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err = media.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return plan, nil
}

func (p *preparedZIP) close() {
	for _, f := range []*os.File{p.transcript, p.media} {
		if f != nil {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
		}
	}
}

func (s *Server) writePreparedZIP(ctx context.Context, w io.Writer, plan *preparedZIP) error {
	z := zip.NewWriter(w)
	tw, err := z.CreateHeader(&zip.FileHeader{Name: "_chat.txt", Method: zip.Store})
	if err != nil {
		return err
	}
	textBuffer := bufio.NewWriterSize(tw, 64<<10)
	if err = copyContext(ctx, textBuffer, plan.transcript); err != nil {
		return err
	}
	if err = textBuffer.Flush(); err != nil {
		return err
	}
	manifest, err := os.CreateTemp("", "whatsapp-archiver-missing-media-*")
	if err != nil {
		return err
	}
	manifestName := manifest.Name()
	defer func() { _ = os.Remove(manifestName) }()
	decoder := json.NewDecoder(plan.media)
	for {
		var item preparedZIPMedia
		if err = decoder.Decode(&item); err == io.EOF {
			break
		}
		if err != nil {
			_ = manifest.Close()
			return err
		}
		path, ok := s.insideMediaDir(item.Path)
		if item.State != "done" || !ok {
			state := item.State
			if state == "done" {
				state = "missing"
			}
			if _, err = fmt.Fprintf(manifest, "%s\t%s\t%s\n", item.ID, item.Name, state); err != nil {
				_ = manifest.Close()
				return err
			}
			continue
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			if _, err = fmt.Fprintf(manifest, "%s\t%s\tmissing\n", item.ID, item.Name); err != nil {
				_ = manifest.Close()
				return err
			}
			continue
		}
		zw, writeErr := z.CreateHeader(&zip.FileHeader{Name: item.Name, Method: zip.Store})
		if writeErr == nil {
			writeErr = copyContext(ctx, zw, f)
		}
		closeErr := f.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = manifest.Close()
			return writeErr
		}
	}
	if _, err = manifest.Seek(0, io.SeekStart); err != nil {
		_ = manifest.Close()
		return err
	}
	mw, err := z.Create("missing-media.txt")
	if err == nil {
		err = copyContext(ctx, mw, manifest)
	}
	closeErr := manifest.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return z.Close()
}

func (s *Server) eachExportMedia(ctx context.Context, x exportScope, fn func(store.ExportRecord) error) error {
	x.snapshot.ResetMediaNames()
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
