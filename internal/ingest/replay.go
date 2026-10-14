package ingest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

func Load(dir string) ([]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []any
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var item recorded
		if err = json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		evt, err := decodeEvent(item)
		if err != nil {
			return nil, err
		}
		out = append(out, evt)
	}
	return out, nil
}
func Replay(ctx context.Context, p *Pipeline, items []any) error {
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.Handle(item)
	}
	return nil
}
