package wa

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// MediaRef identifies the message of a media row, exactly as stored.
type MediaRef struct {
	Chat, Sender, ID string
	FromMe           bool
}

// ErrNoMediaKey marks a row whose raw proto holds no media key; a re-upload can never be requested for it.
var ErrNoMediaKey = errors.New("no media key")

func (c *Client) RequestMediaRetry(ctx context.Context, ref MediaRef, raw []byte) error {
	info, key, err := retryReceipt(ref, raw)
	if err != nil {
		return err
	}
	if err = c.client.SendMediaRetryReceipt(ctx, info, key); err != nil {
		return fmt.Errorf("send media retry receipt for %s: %w", ref.ID, err)
	}
	return nil
}

// retryReceipt builds the message info and media key SendMediaRetryReceipt needs from a media row.
func retryReceipt(ref MediaRef, raw []byte) (*types.MessageInfo, []byte, error) {
	chat, err := types.ParseJID(ref.Chat)
	if err != nil {
		return nil, nil, fmt.Errorf("parse chat %q: %w", ref.Chat, err)
	}
	sender, err := types.ParseJID(ref.Sender)
	if err != nil {
		return nil, nil, fmt.Errorf("parse sender %q: %w", ref.Sender, err)
	}
	media, err := rawMedia(raw, "")
	if err != nil {
		return nil, nil, err
	}
	if media == nil || len(media.GetMediaKey()) == 0 {
		return nil, nil, fmt.Errorf("media %s: %w", ref.ID, ErrNoMediaKey)
	}
	info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: ref.FromMe, IsGroup: chat.Server == types.GroupServer}, ID: types.MessageID(ref.ID)}
	return info, media.GetMediaKey(), nil
}

func (c *Client) ApplyMediaRetry(evt *events.MediaRetry, raw []byte) (string, error) {
	media, err := rawMedia(raw, "")
	if err != nil {
		return "", err
	}
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
