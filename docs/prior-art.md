# Phase 0 prior-art intake

Research completed 2026-10-14 against openclaw/wacli `c3e728ffa3e1`,
mautrix/whatsapp `73b70f2753a4`, and whatsmeow
`v0.0.0-20261013142727-4cf8bc8e0aeb` (`4cf8bc8e0aeb`). The pinned
whatsmeow module requires Go 1.26.0 and recommends toolchain 1.27.1.

## Table 1 — edge cases

| Case | Source and date | Our handling | Test name | Task |
| --- | --- | --- | --- | --- |
| Primary-device re-request is off by default and undecryptable events were silently lost | wacli PR #441, merged 2026-09-30 | Enable `AutomaticMessageRerequestFromPhone`; persist a deterministic gap event and close it when the resent message arrives | `TestUndecryptableThenResentClosesGap` | 5 |
| Full history is opt-in, bounded, chunked, and may continue after the pairing process exits | wacli PR #461, merged 2026-10-10 | Set explicit full-sync limits, keep capture running, and ledger every chunk | `TestLateChunkAfterCompleteIsStored` | 6 |
| LID mappings can race backfill | mautrix-whatsapp v26.01, 2026-01 | Store chunk mappings before its messages | `TestMappingsInChunkApplyToItsMessages` | 6 |
| History can contain the same DM in LID and phone-number forms | mautrix-whatsapp v26.01, 2026-01 | Alias-aware insert dedup and resolved read view | `TestSameMessageUnderLIDAndPhoneStoredOnce` | 5 |
| App-state sync can need recovery | mautrix-whatsapp v26.02/v26.09, 2026 | Preserve archive, mute, and pin events; rely on whatsmeow app-state recovery | `TestChatEventsStored` | 5 |
| Backfill can arrive without a full-history sync | mautrix-whatsapp v26.04, 2026-04 | Import every history event independent of sync type | `TestLateChunkAfterCompleteIsStored` | 6 |
| Edit encryption/message wrapping changed | mautrix-whatsapp v26.05, 2026-05 | Normalize both protocol edit shapes | `TestProtocolEditOldAndNewFormats` | 3 |
| New poll message versions appeared | mautrix-whatsapp v26.07, 2026-07 | Normalize current and newer poll-vote shapes | `TestPollVoteCurrentAndNewVersions` | 3 |
| Phone can emit nonsensical history timestamps | mautrix-whatsapp v26.09, 2026-09 | Store timestamps unchanged and continue | `TestNonsensicalHistoryTimestampStoredAsGiven` | 6 |
| Nil/partial history messages caused a logout-race panic during backfill | mautrix-whatsapp PR #955, merged 2026-10-06 | Skip malformed entries without panicking | `TestEmptyOrPartialChunkDoesNotPanic` | 6 |
| Old-media requests can flood during backfill | mautrix-whatsapp issue #932, 2026-06-17 | Limit expired-media re-requests to ten per minute | `TestExpiredRequestsAreRateLimited` | 7 |
| History and live delivery overlap | wacli sync handler, read 2026-10-14 | Route both through one alias-aware idempotent ingest path | `TestHistoryThenLiveNoDuplicate` | 6 |
| A media URL can expire with HTTP 404 or 410 | whatsmeow media retry docs, pinned version | Classify as expired, retain the row, request re-upload, then apply retry direct path | `TestExpiredMediaRetryCompletes` | 7 |
| A handler can panic while whatsmeow is delivering synchronously | mautrix PR #955 and phase-0 review focus | Recover at the handler boundary and keep accepting events | `TestHandleRecoversPanic` | 5 |

## Table 2 — pinned whatsmeow names

All rows refer to `v0.0.0-20261013142727-4cf8bc8e0aeb`.

| What | Exact name and signature | File | Pseudo-version |
| --- | --- | --- | --- |
| Fetch latest client version | `whatsmeow.GetLatestVersion(ctx context.Context, httpClient *http.Client) (*store.WAVersionContainer, error)`; apply with `store.SetWAVersion(version)` | `update.go`, `store/version.go` | pinned |
| Full history properties | `waCompanionReg.DeviceProps.RequireFullSync *bool`; `DeviceProps.HistorySyncConfig *DeviceProps_HistorySyncConfig`; `FullSyncDaysLimit *uint32`; `FullSyncSizeMbLimit *uint32` | `proto/waCompanionReg/WACompanionReg.pb.go` | pinned |
| Re-request undecryptable messages from own phone | `Client.AutomaticMessageRerequestFromPhone bool` | `client.go`, `retry.go` | pinned |
| Ack only after handlers return | `Client.SynchronousAck bool` | `client.go`, `receipt.go`, `message.go` | pinned |
| Message event | `events.Message` (`Info types.MessageInfo`, `Message`, `RawMessage *waE2E.Message`) | `types/events/events.go` | pinned |
| Decryption-gap event | `events.UndecryptableMessage`; failure mode is `DecryptFailMode events.DecryptFailMode` | `types/events/events.go` | pinned |
| History event | `events.HistorySync{Data *waHistorySync.HistorySync, Notification *waE2E.HistorySyncNotification}` | `types/events/events.go` | pinned |
| Connection and account events | `events.ClientOutdated`, `TemporaryBan`, `LoggedOut`, `StreamReplaced`, `Connected`, `Disconnected` | `types/events/events.go` | pinned |
| Media retry event | `events.MediaRetry` | `types/events/events.go` | pinned |
| Group events | `events.GroupInfo`, `events.JoinedGroup` | `types/events/events.go` | pinned |
| App-state events | `events.Archive`, `events.Mute`, `events.Pin` | `types/events/appstate.go` | pinned |
| Alternate message JIDs | `types.MessageInfo.MessageSource.SenderAlt`, `RecipientAlt`, and `AddressingMode` | `types/message.go`, `message.go` | pinned |
| Manual history download | `Client.ManualHistorySyncDownload bool` (the notification then reaches handlers as `*events.Message` with `Message.GetProtocolMessage().GetHistorySyncNotification()`, only when `Info.IsFromMe`; `DisableManualHistorySyncReceipt` left false) | `client.go:109`, `message.go:888-906,1172-1178` | pinned |
| Download a history chunk | `(*whatsmeow.Client).DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, synchronousStorage bool) (*waHistorySync.HistorySync, error)`; wraps download errors with `%w`, so `ErrMediaDownloadFailedWith404`/`410` match with `errors.Is` | `message.go:783` | pinned |
| Notification fields for logs | `waE2E.HistorySyncNotification.GetSyncType() waE2E.HistorySyncType`, `GetChunkOrder() uint32` | `proto/waE2E/WAWebProtobufsE2E.pb.go` | pinned |
| QR pairing channel (Task 10, phase 2) | `(*whatsmeow.Client).GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)`: must be called before the first connect; refuses when connected (`ErrQRAlreadyConnected`) or when `Store.ID` is set (`ErrQRStoreContainsID`); buffer 8; codes 60 s then 20 s each, six per connection, then `QRChannelTimeout` and a disconnect; a full output channel ends pairing; ctx end closes it. `QRChannelItem{Event, Error, Code, Timeout}`, `QRChannelEventCode`, `QRChannelSuccess`, `QRChannelTimeout`, `QRChannelEventError`, `err-*` items | `qrchan.go:22-56,74-134,189-212,215-240` | pinned |
| Pair success and the reconnect | `events.PairSuccess` is dispatched after `handlePair` saved `Store.ID` and sent `pair-device-sign`; the server then ends the stream with code 515 and whatsmeow reconnects by itself, logged in (unless `DisableLoginAutoReconnect`) | `pair.go:132-160,222-241`, `connectionevents.go:26-39` | pinned |
| Remove an event handler | `(*whatsmeow.Client).RemoveEventHandler(id uint32) bool` (id from `AddEventHandlerWithSuccessStatus`); not from inside a handler. With no handler left, `dispatchEvent` reports success, so an event would be acked: only a pairing that never completed removes its handler | `client.go:817-856,996-1011` | pinned |
| Handler verdict skips the ack | `whatsmeow.EventHandlerWithSuccessStatus func(evt any) bool`, `(*Client).AddEventHandlerWithSuccessStatus`; a false makes `dispatchEvent` report failure and `decryptMessages` returns before the receipt | `client.go:48,817,996-1011`, `message.go:453,459-462,489-497` | pinned |
| History LID mappings | `HistorySync.GetPhoneNumberToLidMappings() []*waHistorySync.PhoneNumberToLIDMapping`; conversation IDs remain in `Conversation.ID` | `proto/waHistorySync/...pb.go`, `message.go` | pinned |
| Parse a history message | `(*whatsmeow.Client).ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)` | `client.go` | pinned |
| Download media | `(*whatsmeow.Client).Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)` | `download.go` | pinned |
| Expired-media errors | `whatsmeow.ErrMediaDownloadFailedWith404`, `whatsmeow.ErrMediaDownloadFailedWith410` | `errors.go` | pinned |
| Request media re-upload | `(*whatsmeow.Client).SendMediaRetryReceipt(ctx context.Context, message *types.MessageInfo, mediaKey []byte) error` | `mediaretry.go` | pinned |
| Decrypt media retry | `whatsmeow.DecryptMediaRetryNotification(evt *events.MediaRetry, mediaKey []byte) (*waMmsRetry.MediaRetryNotification, error)` | `mediaretry.go` | pinned |
| Session SQL store | `sqlstore.New(ctx context.Context, dialect, address string, log waLog.Logger) (*Container, error)`; modernc dialect `sqlite`, DSN `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate` (`internal/wa/session.go`) | `store/sqlstore/container.go` | pinned |
| Why a message could not be decrypted (W2c) | `events.UndecryptableMessage{Info types.MessageInfo; IsUnavailable bool; UnavailableType events.UnavailableType; DecryptFailMode events.DecryptFailMode}`; constants `events.UnavailableTypeUnknown` (`""`) and `events.UnavailableTypeViewOnce` (`"view_once"`). `IsUnavailable` is true when no ciphertext arrived at all (an `unavailable` node) or when a group message failed with `signalerror.ErrNoSenderKeyForUser`. The event carries no error value, so an untrusted identity cannot be told from any other failure: `cause` is `view_once_unavailable` for `UnavailableTypeViewOnce`, `no_sender_key` for `IsUnavailable`, else `other` (`untrusted_identity` stays in the vocabulary, never produced) | `types/events/events.go:281-311`, `message.go:331-333,419-436` | pinned |
| Message event flags (W2c) | `events.Message.IsEphemeral`, `IsViewOnce`, `IsViewOnceV2`, `IsDocumentWithCaption`, `IsEdit` and `(*events.Message).UnwrapRaw() *events.Message`: set when the message was unwrapped from the matching wrapper; a replayed event is rebuilt with `UnwrapRaw` | `types/events/events.go:322-347,377-425` | pinned |
| Decrypt a poll vote locally (W2c, R15) | `(*whatsmeow.Client).DecryptPollVote(ctx context.Context, vote *events.Message) (*waE2E.PollVoteMessage, error)`: reads the poll's message secret with `Store.MsgSecrets.GetMessageSecret` (session.db), decrypts with AES-GCM, sends nothing; `ErrOriginalMessageSecretNotFound` when the secret is missing; `waE2E.PollVoteMessage.GetSelectedOptions() [][]byte` holds SHA-256 hashes of the option names (`whatsmeow.HashPollOptions` hashes the same way; ingest computes the hash itself because only `internal/wa` imports the client) | `msgsecret.go:305-320,130-192,369-377`, `errors.go:149`, `proto/waE2E/WAWebProtobufsE2E.pb.go:13271` | pinned |
| Message secret store, tests only (W2c) | `store.MsgSecretStore{PutMessageSecrets; PutMessageSecret(ctx, chat, sender types.JID, id types.MessageID, secret []byte) error; GetMessageSecret(ctx, chat, sender, id) ([]byte, types.JID, error)}`, `store.Device.MsgSecrets`; `(*Client).EncryptPollVote(ctx, pollInfo *types.MessageInfo, vote *waE2E.PollVoteMessage) (*waE2E.PollUpdateMessage, error)` (local encryption, no send) builds the encrypted vote of the `internal/wa` test | `store/store.go:135-139,251`, `msgsecret.go:423-445` | pinned |
| Reactions and poll updates in a history message (W2c, R14) | `waWeb.WebMessageInfo.GetReactions() []*waWeb.Reaction` (`Key` is the reaction's own key: ID, `Participant`, `FromMe`; `Text`; `SenderTimestampMS`) and `GetPollUpdates() []*waWeb.PollUpdate` (`PollUpdateMessageKey` is the update's own key; `Vote *waE2E.PollVoteMessage`, already decrypted by the phone; `SenderTimestampMS`). The parent message is the target. Each is parsed with `ParseWebMessage` as a synthetic message whose key is the reaction's or update's own | `proto/waWeb/WAWebProtobufsWeb.pb.go:1856,1884,3394,3470` | pinned |
| Message content fields read by the normaliser (W2c) | `waE2E.Message`: `GetConversation`, `GetExtendedTextMessage`, `GetImageMessage`, `GetVideoMessage`, `GetPtvMessage` (video note), `GetAudioMessage`, `GetDocumentMessage`, `GetStickerMessage`, `GetLocationMessage`, `GetLiveLocationMessage`, `GetContactMessage`, `GetContactsArrayMessage`, `GetPollCreationMessage`, `...V2`, `...V3`, `...V5`, `...V6`, `GetPollCreationMessageV4` (a `FutureProofMessage` wrapper), `GetEventMessage`, `GetCallLogMesssage` (sic), `GetScheduledCallCreationMessage`, `GetScheduledCallEditMessage`, `GetBcallMessage`, `GetCall`, `GetViewOnceMessage`, `GetViewOnceMessageV2`, `GetViewOnceMessageV2Extension`. Media: `GetMimetype`, `GetFileLength`, `GetWidth`, `GetHeight`, `GetSeconds`, `GetPTT`, `GetGifPlayback`, `GetPageCount`, `GetFileName`, `GetTitle`, `GetCaption`, `GetJPEGThumbnail` (sticker: `GetPngThumbnail`), `GetIsAnimated`, `GetViewOnce`. Text: `ExtendedTextMessage.GetMatchedText/GetTitle/GetDescription`. Others: `LocationMessage.GetDegreesLatitude/Longitude/Name/Address/URL`, `LiveLocationMessage.GetDegreesLatitude/GetDegreesLongitude/GetAccuracyInMeters/GetSpeedInMps/GetSequenceNumber/GetCaption`, `ContactMessage.GetDisplayName/GetVcard`, `ContactsArrayMessage.GetDisplayName/GetContacts`, `PollCreationMessage.GetName/GetOptions/GetSelectableOptionsCount`, `PollCreationMessage_Option.GetOptionName`, `EventMessage.GetName/GetDescription/GetStartTime/GetEndTime/GetLocation (then LocationMessage.GetName)/GetIsCanceled`. Context: `ContextInfo.GetStanzaID/GetParticipant/GetMentionedJID/GetIsForwarded/GetForwardingScore/GetExpiration` and every content type's `GetContextInfo()` | `proto/waE2E/WAWebProtobufsE2E.pb.go` | pinned |

## Table 3 — borrowed code

| wacli file → our file | Licence | What changed |
| --- | --- | --- |
| `internal/wa/client.go` → `internal/wa/props.go` | MIT | Adapt only the bounded full-history property setup to this project's config types and receive-only wrapper |
| `internal/app/sync_events.go` → `internal/ingest/history.go` | MIT | Reduce to history parsing through the single ingest path; add immutable chunk ledger, alias-first mapping, exclusions, and timestamp preservation |
| `internal/app/sync_recovery.go` → `internal/ingest/pipeline.go` | MIT | Preserve decryption gaps in the raw event table; whatsmeow owns retry scheduling |

No mautrix-whatsapp code is copied; it is AGPL-3.0 and was consulted only for
edge-case ideas. Any adapted wacli code retains its MIT header and is listed in
`NOTICE`.
