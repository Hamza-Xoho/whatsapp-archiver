package web

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"whatsapp-archiver/internal/store"
)

func (s *Server) zipMediaNames(ctx context.Context, x exportScope) (map[string]string, error) {
	counts := map[string]int{}
	if err := s.eachExport(ctx, x, func(r store.ExportRecord) error {
		if r.Media != nil {
			counts[safeMediaName(r.Media.Filename, r.Message.ID)]++
		}
		return nil
	}); err != nil {
		return nil, err
	}
	seen := map[string]int{}
	names := map[string]string{}
	err := s.eachExport(ctx, x, func(r store.ExportRecord) error {
		if r.Media == nil {
			return nil
		}
		base := safeMediaName(r.Media.Filename, r.Message.ID)
		seen[base]++
		name := base
		if counts[base] > 1 && seen[base] > 1 {
			name = uniqueName(base, seen[base])
		}
		names[r.Message.ID] = name
		return nil
	})
	return names, err
}

func uniqueName(name string, n int) string {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)] + "_" + strconv.Itoa(n) + ext
}

func (s *Server) streamZIP(ctx context.Context, w io.Writer, x exportScope) error {
	names, err := s.zipMediaNames(ctx, x)
	if err != nil {
		return err
	}
	z := zip.NewWriter(w)
	manifest, err := z.Create("missing-media.txt")
	if err != nil {
		return err
	}
	err = s.eachExport(ctx, x, func(r store.ExportRecord) error {
		m := r.Media
		if m == nil {
			return nil
		}
		path, ok := s.insideMediaDir(m.Path)
		if ok && m.State == "done" {
			if st, e := os.Stat(path); e == nil && st.Mode().IsRegular() {
				return nil
			}
		}
		_, e := fmt.Fprintf(manifest, "%s\t%s\t%s\n", r.Message.ID, names[r.Message.ID], m.State)
		return e
	})
	if err != nil {
		return err
	}
	tw, err := z.Create("_chat.txt")
	if err != nil {
		return err
	}
	if err = s.streamTextWithNames(ctx, tw, x, names); err != nil {
		return err
	}
	err = s.eachExport(ctx, x, func(r store.ExportRecord) error {
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
		zw, e := z.Create(names[r.Message.ID])
		if e == nil {
			_, e = io.Copy(zw, f)
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
