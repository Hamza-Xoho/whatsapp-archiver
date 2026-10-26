package wa

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// secretStore is an in-memory MsgSecretStore: the part of session.db that holds a poll's message secret.
type secretStore struct {
	mu      sync.Mutex
	secrets map[string][]byte
}

func (s *secretStore) key(chat, sender types.JID, id types.MessageID) string {
	return chat.String() + "|" + sender.String() + "|" + string(id)
}
func (s *secretStore) PutMessageSecrets(context.Context, []wmstore.MessageSecretInsert) error {
	return errors.New("not used")
}
func (s *secretStore) PutMessageSecret(_ context.Context, chat, sender types.JID, id types.MessageID, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secrets == nil {
		s.secrets = map[string][]byte{}
	}
	s.secrets[s.key(chat, sender, id)] = secret
	return nil
}
func (s *secretStore) GetMessageSecret(_ context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets[s.key(chat, sender, id)], sender, nil
}

func hashOf(name string) []byte { h := sha256.Sum256([]byte(name)); return h[:] }

// voteFixture is a poll sent by pollSender in a group and a vote on it by voter, encrypted the way a phone does.
func voteFixture(t *testing.T) (c *Client, vote *events.Message, noSecret *events.Message) {
	t.Helper()
	pn := types.JID{User: "111", Server: types.DefaultUserServer}
	lid := types.JID{User: "222", Server: types.HiddenUserServer}
	secrets := &secretStore{}
	own := whatsmeow.NewClient(&wmstore.Device{ID: &pn, LID: lid, MsgSecrets: secrets}, nil)
	group := types.JID{User: "g", Server: types.GroupServer}
	pollSender := types.JID{User: "333", Server: types.HiddenUserServer}
	pollInfo := types.MessageInfo{MessageSource: types.MessageSource{Chat: group, Sender: pollSender, IsGroup: true}, ID: "POLL"}
	if err := secrets.PutMessageSecret(t.Context(), group, pollSender, "POLL", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	// The owner of the client votes (EncryptPollVote encrypts as the client's own LID), so the receiving side is built
	// from the same secret store and the vote's sender is that LID.
	enc, err := own.EncryptPollVote(t.Context(), &pollInfo, &waE2E.PollVoteMessage{SelectedOptions: [][]byte{hashOf("Yes"), hashOf("Maybe")}})
	if err != nil {
		t.Fatal(err)
	}
	msg := &waE2E.Message{PollUpdateMessage: enc}
	vote = &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: group, Sender: lid, IsGroup: true}, ID: "VOTE"}, Message: msg, RawMessage: msg}
	other := &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: &waCommon.MessageKey{ID: proto.String("GONE"), Participant: proto.String(pollSender.String())}, Vote: enc.Vote}}
	noSecret = &events.Message{Info: vote.Info, Message: other, RawMessage: other}
	return &Client{client: own}, vote, noSecret
}

// R15: the vote is decrypted locally with the poll's message secret from session.db; nothing is sent.
func TestVoteDecryptorReturnsTheSelectedOptionHashes(t *testing.T) {
	c, vote, _ := voteFixture(t)
	got, err := c.VoteDecryptor()(t.Context(), vote)
	if err != nil || len(got) != 2 || string(got[0]) != string(hashOf("Yes")) || string(got[1]) != string(hashOf("Maybe")) {
		t.Fatalf("hashes=%x err=%v", got, err)
	}
}

func TestVoteDecryptorFailsWithoutThePollSecret(t *testing.T) {
	c, _, noSecret := voteFixture(t)
	if got, err := c.VoteDecryptor()(t.Context(), noSecret); err == nil || got != nil {
		t.Fatalf("hashes=%x err=%v, want an error", got, err)
	}
}

func TestVoteDecryptorRefusesAMessageThatIsNotAVote(t *testing.T) {
	c, _, _ := voteFixture(t)
	msg := &waE2E.Message{Conversation: proto.String("hi")}
	if _, err := c.VoteDecryptor()(t.Context(), &events.Message{Message: msg, RawMessage: msg}); err == nil {
		t.Fatal("want an error")
	}
}
