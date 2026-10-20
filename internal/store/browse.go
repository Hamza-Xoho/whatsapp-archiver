package store

import (
	"context"
	"errors"
)

var errNotImplemented = errors.New("not implemented")

// ChatRow is one line of the chats screen.
type ChatRow struct {
	ChatStat
	DisplayName  string
	Logging      bool
	HasMediaRule bool
	New          bool
}

// ViewMessage is one message as the chat viewer shows it.
type ViewMessage struct {
	ID, Sender, SenderName, Text, EditedText, MediaType, MediaState string
	TS                                                              int64
	Deleted                                                         bool
	Reactions                                                       []string
	QuotedID                                                        string
}

// Trace is everything the archive holds about one message.
type Trace struct {
	Message Message
	Events  []MessageEvent
	Media   *Media
	Gap     bool
}

func (s *Store) ChatList(ctx context.Context, filter string) ([]ChatRow, error) {
	return nil, errNotImplemented
}

func (s *Store) ChatPage(ctx context.Context, canonicalJID string, beforeTS int64, limit int) ([]ViewMessage, error) {
	return nil, errNotImplemented
}

func (s *Store) Trace(ctx context.Context, chatJID, id string) (Trace, error) {
	return Trace{}, errNotImplemented
}
