package wa

import (
	"bytes"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func marshal(t *testing.T, m *waE2E.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// H3: the re-upload request carries the row's message identity and the media key from its raw proto (wrappers unwrapped).
func TestRequestMediaRetryReceivesMessageInfoAndKey(t *testing.T) {
	wrapped := marshal(t, &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{MediaKey: []byte{1, 2, 3}}}}})
	info, key, err := retryReceipt(MediaRef{Chat: "123@g.us", Sender: "5@lid", ID: "ABC"}, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	want := types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("123", types.GroupServer), Sender: types.NewJID("5", types.HiddenUserServer), IsGroup: true}, ID: "ABC"}
	if info.Chat != want.Chat || info.Sender != want.Sender || info.IsGroup != want.IsGroup || info.IsFromMe || info.ID != want.ID || !bytes.Equal(key, []byte{1, 2, 3}) {
		t.Fatalf("info=%+v key=%v", info, key)
	}
	own := marshal(t, &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{MediaKey: []byte{9}}}}})
	info, key, err = retryReceipt(MediaRef{Chat: "7@s.whatsapp.net", Sender: "1@s.whatsapp.net", ID: "OWN", FromMe: true}, own)
	if err != nil || !info.IsFromMe || info.IsGroup || info.Chat != types.NewJID("7", types.DefaultUserServer) || info.ID != "OWN" || !bytes.Equal(key, []byte{9}) {
		t.Fatalf("info=%+v key=%v err=%v", info, key, err)
	}
}

// A row whose raw proto has no media key cannot be re-requested.
func TestRequestMediaRetryWithoutKeyIsErrNoMediaKey(t *testing.T) {
	for name, m := range map[string]*waE2E.Message{
		"no key":   {ImageMessage: &waE2E.ImageMessage{}},
		"no media": {Conversation: proto.String("hi")},
	} {
		if _, _, err := retryReceipt(MediaRef{Chat: "123@g.us", Sender: "5@lid", ID: "X"}, marshal(t, m)); !errors.Is(err, ErrNoMediaKey) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}
