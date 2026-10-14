package health

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/retry"
)

type Notifier interface {
	Notify(context.Context, string, string) error
}
type ntfy struct {
	cfg    *config.NtfyConfig
	client *http.Client
}

func NewNtfy(cfg *config.NtfyConfig) Notifier {
	return &ntfy{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}
func (n *ntfy) Notify(ctx context.Context, title, body string) error {
	_, err := retry.Do(ctx, retry.Policy{Max: 5, Base: time.Second}, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.URL, bytes.NewBufferString(body))
		if err != nil {
			return err
		}
		req.Header.Set("Title", title)
		if n.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+n.cfg.Token)
		}
		resp, err := n.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("ntfy status %s", resp.Status)
		}
		return nil
	})
	return err
}
