package ingest

import (
	"time"
	"whatsapp-archiver/internal/store"
)

type Kind string

const (
	KindMessage         Kind = "message"
	KindEdit            Kind = "edit"
	KindRevoke          Kind = "revoke"
	KindProtocol        Kind = "protocol" // any other protocol message, Value = its type name
	KindReaction        Kind = "reaction"
	KindPin             Kind = "pin"
	KindPollVote        Kind = "poll_vote"
	KindUndecryptable   Kind = "undecryptable"
	KindSecretEncrypted Kind = "secret_encrypted" // a non-edit SecretEncryptedMessage, Value = its type name
	KindGroupInfo       Kind = "group_info"
	KindParticipants    Kind = "participants"
	KindAppState        Kind = "app_state"
	KindPresence        Kind = "presence"
	KindTyping          Kind = "typing"
	KindReceipt         Kind = "receipt"
	KindCall            Kind = "call"
)

type Result struct {
	Kind      Kind
	Chat      store.Chat
	Sender    store.Participant
	Message   *store.Message
	Event     *store.MessageEvent
	ChatEvent *store.ChatEvent
	Media     *store.Media
	Mappings  []store.IDMapping
	FromMe    bool
	Received  time.Time
}
