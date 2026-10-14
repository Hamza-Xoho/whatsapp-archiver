package wa

import (
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// NewHistoryParser parses history messages for the account with these own IDs; it never connects.
func NewHistoryParser(ownPN, ownLID types.JID) func(types.JID, *waWeb.WebMessageInfo) (*events.Message, error) {
	return historyParser(whatsmeow.NewClient(&wmstore.Device{ID: &ownPN, LID: ownLID}, nil))
}

// HistoryParser parses history messages with this client's own IDs, read at parse time.
func (c *Client) HistoryParser() func(types.JID, *waWeb.WebMessageInfo) (*events.Message, error) {
	return historyParser(c.client)
}

// historyParser wraps ParseWebMessage, which rewrites an edit into the edited message under the original ID;
// an edit is kept as the edit message under its own ID instead.
func historyParser(cli *whatsmeow.Client) func(types.JID, *waWeb.WebMessageInfo) (*events.Message, error) {
	return func(chat types.JID, web *waWeb.WebMessageInfo) (*events.Message, error) {
		edit := (&events.Message{RawMessage: web.GetMessage()}).UnwrapRaw().Message
		evt, err := cli.ParseWebMessage(chat, web)
		if err != nil {
			return nil, err
		}
		if edit.GetProtocolMessage().GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT {
			evt.Info.ID, evt.Message = web.GetKey().GetID(), edit
		}
		return evt, nil
	}
}
