// Package archiveread supplies bounded read-only evidence from supported archives.
package archiveread

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Policy struct {
	Mode       string   `yaml:"mode" json:"mode"`
	AllowChats []string `yaml:"allow_chats" json:"allow_chats"`
	DenyChats  []string `yaml:"deny_chats" json:"deny_chats"`
}
type Config struct {
	Archive       string `yaml:"archive"`
	RuntimeDir    string `yaml:"runtime_dir"`
	ViewerBaseURL string `yaml:"viewer_base_url"`
	Policy        Policy `yaml:"policy"`
}

type codedError struct{ code string }

func (e *codedError) Error() string { return e.code }
func failure(code string) error     { return &codedError{code: code} }

// CodeOf returns a stable redacted error code; it never exposes database diagnostics.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return "INTERNAL_ERROR"
}

func LoadConfig(path string) (Config, error) {
	var c Config
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		return c, failure("INVALID_ARGUMENT")
	}
	f, e := os.Open(path)
	if e != nil {
		return c, failure("INVALID_ARGUMENT")
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return c, failure("INVALID_ARGUMENT")
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if e = dec.Decode(&c); e != nil {
		return c, failure("INVALID_ARGUMENT")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return c, failure("INVALID_ARGUMENT")
	}
	if !filepath.IsAbs(c.Archive) || !filepath.IsAbs(c.RuntimeDir) || strings.EqualFold(filepath.Base(c.Archive), "session.db") || (c.Policy.Mode != "selected" && c.Policy.Mode != "all") {
		return Config{}, failure("INVALID_ARGUMENT")
	}
	if len(c.Policy.AllowChats)+len(c.Policy.DenyChats) > 10000 {
		return Config{}, failure("INVALID_ARGUMENT")
	}
	for _, list := range [][]string{c.Policy.AllowChats, c.Policy.DenyChats} {
		for _, id := range list {
			if id == "" || len(id) > 512 || strings.ContainsAny(id, "\x00\r\n") {
				return Config{}, failure("INVALID_ARGUMENT")
			}
		}
	}
	if c.ViewerBaseURL != "" {
		u, err := url.Parse(c.ViewerBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Config{}, failure("INVALID_ARGUMENT")
		}
	}
	return c, nil
}
