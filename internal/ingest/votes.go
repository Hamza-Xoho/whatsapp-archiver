package ingest

import (
	"context"

	"go.mau.fi/whatsmeow/types/events"
)

// VoteDecryptor decrypts a live poll vote with the poll's message secret and returns the SHA-256 hashes of the options it
// selected. internal/wa provides it (R15): it reads the secret from session.db and sends nothing.
type VoteDecryptor func(ctx context.Context, vote *events.Message) ([][]byte, error)

// pollSource is a writer that can say which options a stored poll has.
type pollSource interface {
	PollOptions(ctx context.Context, chat, id string) ([]string, bool, error)
}

// WithVoteDecryptor sets the decryptor Handle uses for the poll votes it receives live. History and replay never use
// it: history poll updates arrive decrypted, and a replay has no session.
func (p *Pipeline) WithVoteDecryptor(d VoteDecryptor) *Pipeline {
	p.votes = d
	return p
}

// nameVote sets the value of a live poll_vote event to {"options":[...]}, the options the vote selected by name. The
// value stays empty when the vote cannot be decrypted, the poll is not in the archive, or none of the hashes is an option
// of the poll; the event is stored all the same.
func (p *Pipeline) nameVote(ctx context.Context, evt *events.Message, r *Result) {
	if p.votes == nil || r.Kind != KindPollVote || r.Event == nil {
		return
	}
	src, ok := p.writer.(pollSource)
	if !ok {
		return
	}
	selected, err := p.votes(ctx, evt)
	if err != nil {
		p.log.Debug("poll vote not decrypted", "chat", r.Chat.JID, "id", r.Event.SourceID, "err", err)
		return
	}
	options, found, err := src.PollOptions(ctx, r.Chat.JID, r.Event.TargetID)
	if err != nil || !found {
		p.log.Debug("poll of a vote not in the archive", "chat", r.Chat.JID, "poll", r.Event.TargetID, "err", err)
		return
	}
	r.Event.Value = voteValue(options, selected)
}
