package wa

import (
	"context"

	"go.mau.fi/whatsmeow/types/events"
)

// VoteDecryptor returns the function that decrypts a poll vote locally (R15): whatsmeow reads the poll's message secret
// from session.db, which it stored when the poll arrived, and decrypts the vote with it. Nothing is sent. The result is
// the SHA-256 hashes of the options the vote selected; ingest names them from the archived poll.
func (c *Client) VoteDecryptor() func(context.Context, *events.Message) ([][]byte, error) {
	return func(ctx context.Context, vote *events.Message) ([][]byte, error) {
		decrypted, err := c.client.DecryptPollVote(ctx, vote)
		if err != nil {
			return nil, err
		}
		return decrypted.GetSelectedOptions(), nil
	}
}
