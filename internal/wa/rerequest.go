package wa

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

type MediaRef struct {
	Info     types.MessageInfo
	MediaKey []byte
}

func (c *Client) RequestMediaRetry(ctx context.Context, ref MediaRef, _ []byte) error {
	return c.client.SendMediaRetryReceipt(ctx, &ref.Info, ref.MediaKey)
}
func (c *Client) ApplyMediaRetry(evt *events.MediaRetry, raw []byte) (string, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return "", err
	}
	media := mediaMessage(&msg, "")
	if media == nil {
		return "", fmt.Errorf("message has no media key")
	}
	result, err := whatsmeow.DecryptMediaRetryNotification(evt, media.GetMediaKey())
	if err != nil {
		return "", fmt.Errorf("decrypt media retry: %w", err)
	}
	if result.GetDirectPath() == "" {
		return "", fmt.Errorf("media retry returned no direct path")
	}
	return result.GetDirectPath(), nil
}
