package ingest

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// One rule for key material (I3 and I4, Hamza's rulings of 26 Oct 2026): archive.db is backed up and messages and
// message_events are insert-only, so no secret may be written to it, whether it comes live (FromMessage, FromStub), from
// replay or from a history chunk (StripHistorySecrets before the spool). stripMessage is that rule. It clears by field
// name, so a new secret field is caught when its name matches. Residual risk: protobuf fields unknown to the pinned
// protos stay as they are, and so do opaque bytes such as WebMessageInfo.futureproofData, which this rule cannot
// look into; a secret in a field newer than the pinned protos would be stored. The bump-whatsmeow skill re-diffs the
// bytes fields against secretFieldWords on every bump.

// secretFieldWords are lower-case fragments of bytes-field names whose value is key material or a secret token. A
// bytes field in any message at any depth (nested, quoted, edited, wrapped, a reaction or a poll update, a status) is
// cleared when its name contains one. Examples: messageSecret, botMessageSecret, threadIDUserSecret, rootSecret,
// tcToken, acAuthTokens, nctSalt, shareableChatIdentifierEncryptionKey, contactPrimaryIdentityKey, encKey (a poll's),
// revealKey, callKey, keyData, groupRootKey, reportingTag, axolotlSenderKeyDistributionMessage. mediaKey,
// fileEncSHA256 and fileSHA256 are not in the list.
var secretFieldWords = []string{
	"secret", "token", "salt", "enckey", "encryptionkey", "revealkey", "callkey", "keydata", "masterkey", "grouprootkey",
	"frankingkey", "privatekey", "identitykey", "reportingtag", "senderkeydistributionmessage",
}

// secretFields are bytes fields cleared by their full name, because their name alone would also catch message media.
var secretFields = map[protoreflect.FullName]bool{
	// A saved sticker (not a message): its media key is never needed to import history.
	"WAWebProtobufsHistorySync.StickerMetadata.mediaKey": true,
}

// stripMessage clears the key material of m and of every message nested in it, in place.
func stripMessage(m protoreflect.Message) {
	if pm, ok := m.Interface().(*waE2E.ProtocolMessage); ok {
		dropKeyPayloads(pm)
	}
	var fields []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fields = append(fields, fd)
		return true
	})
	for _, fd := range fields {
		switch {
		case fd.Kind() == protoreflect.BytesKind:
			if isSecretField(fd) {
				m.Clear(fd)
			}
		case fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind:
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				m.Get(fd).Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					stripMessage(v.Message())
					return true
				})
			}
		case fd.IsList():
			list := m.Get(fd).List()
			for i := 0; i < list.Len(); i++ {
				stripMessage(list.Get(i).Message())
			}
		default:
			stripMessage(m.Get(fd).Message())
		}
	}
}

func isSecretField(fd protoreflect.FieldDescriptor) bool {
	if secretFields[fd.FullName()] {
		return true
	}
	name := strings.ToLower(string(fd.Name()))
	for _, word := range secretFieldWords {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// dropKeyPayloads removes the payloads of the protocol messages that carry keys (the keyMaterial types of normalize.go):
// a history notification (its media key), an app-state key share, a peer-data response and a contact hash key share.
// The message and its type stay, as the normaliser stores them without raw_proto.
func dropKeyPayloads(pm *waE2E.ProtocolMessage) {
	pm.HistorySyncNotification = nil
	pm.AppStateSyncKeyShare = nil
	pm.PeerDataOperationRequestResponseMessage = nil
	pm.SharedDeviceContactHashKeyShare = nil
}
