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
| History LID mappings | `HistorySync.GetPhoneNumberToLidMappings() []*waHistorySync.PhoneNumberToLIDMapping`; conversation IDs remain in `Conversation.ID` | `proto/waHistorySync/...pb.go`, `message.go` | pinned |
| Parse a history message | `(*whatsmeow.Client).ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)` | `client.go` | pinned |
| Download media | `(*whatsmeow.Client).Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)` | `download.go` | pinned |
| Expired-media errors | `whatsmeow.ErrMediaDownloadFailedWith404`, `whatsmeow.ErrMediaDownloadFailedWith410` | `errors.go` | pinned |
| Request media re-upload | `(*whatsmeow.Client).SendMediaRetryReceipt(ctx context.Context, message *types.MessageInfo, mediaKey []byte) error` | `mediaretry.go` | pinned |
| Decrypt media retry | `whatsmeow.DecryptMediaRetryNotification(evt *events.MediaRetry, mediaKey []byte) (*waMmsRetry.MediaRetryNotification, error)` | `mediaretry.go` | pinned |
| Session SQL store | `sqlstore.New(ctx context.Context, dialect, address string, log waLog.Logger) (*Container, error)`; modernc dialect `sqlite`, DSN `file:<path>?_pragma=foreign_keys(1)` | `store/sqlstore/container.go` | pinned |

## Table 3 — borrowed code

| wacli file → our file | Licence | What changed |
| --- | --- | --- |
| `internal/wa/client.go` → `internal/wa/props.go` | MIT | Adapt only the bounded full-history property setup to this project's config types and receive-only wrapper |
| `internal/app/sync_events.go` → `internal/ingest/history.go` | MIT | Reduce to history parsing through the single ingest path; add immutable chunk ledger, alias-first mapping, exclusions, and timestamp preservation |
| `internal/app/sync_recovery.go` → `internal/ingest/pipeline.go` | MIT | Preserve decryption gaps in the raw event table; whatsmeow owns retry scheduling |

No mautrix-whatsapp code is copied; it is AGPL-3.0 and was consulted only for
edge-case ideas. Any adapted wacli code retains its MIT header and is listed in
`NOTICE`.
