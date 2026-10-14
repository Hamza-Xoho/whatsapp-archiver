package wa

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

var ErrMediaExpired = errors.New("media expired")

func classifyDownloadErr(err error) error {
	if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) || errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
		return ErrMediaExpired
	}
	if err != nil {
		return fmt.Errorf("download media: %w", err)
	}
	return nil
}

func (c *Client) Download(ctx context.Context, raw []byte, directPathOverride string) ([]byte, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("decode media message: %w", err)
	}
	downloadable := mediaMessage(&msg, directPathOverride)
	if downloadable == nil {
		return nil, fmt.Errorf("message has no downloadable media")
	}
	data, err := c.client.Download(ctx, downloadable)
	return data, classifyDownloadErr(err)
}

func mediaMessage(msg *waE2E.Message, path string) whatsmeow.DownloadableMessage {
	if x := msg.GetImageMessage(); x != nil {
		if path != "" {
			x.DirectPath = proto.String(path)
		}
		return x
	}
	if x := msg.GetDocumentMessage(); x != nil {
		if path != "" {
			x.DirectPath = proto.String(path)
		}
		return x
	}
	if x := msg.GetAudioMessage(); x != nil {
		if path != "" {
			x.DirectPath = proto.String(path)
		}
		return x
	}
	if x := msg.GetVideoMessage(); x != nil {
		if path != "" {
			x.DirectPath = proto.String(path)
		}
		return x
	}
	if x := msg.GetStickerMessage(); x != nil {
		if path != "" {
			x.DirectPath = proto.String(path)
		}
		return x
	}
	return nil
}
