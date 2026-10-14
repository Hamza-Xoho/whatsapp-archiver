# Brief: H6b — follow-ups from the H5/H6/H9 review (one red/green cycle)
Same repo/branch/process as h5-h6-h9-brief.md (red `test:` commit → lock → fix → make check → unlock → `fix:` commit → mutations → PROGRESS entry).
1. `UpsertParticipant` must not overwrite a known `display_name` with an empty push name (same rule as H6a: `COALESCE(NULLIF(excluded.display_name,''), display_name)` — check the column name). Red test: `TestEmptyPushNameKeepsKnownDisplayName`. Mutation `h6b-participant`.
2. H9 ruling: an identical redelivered chunk is one ledger row (content hash). Pin it: `TestIdenticalChunkTwiceIsOneLedgerRow` (and messages still stored once). Mutation `h9-dedup` (ID made unique per call, e.g. includes received time) must be KILLED.
3. `history.go` chunk-ID marshal: use `proto.MarshalOptions{Deterministic: true, AllowPartial: true}` and handle the error (wrap and return it from ImportHistory before any write; count nothing). Test with whatever is cheap; at least the existing tests must still pass.
4. `TestExcludedChatNameNotStoredFromHistory` is an existing test you wrote in this branch — do NOT weaken it; ADD a new test `TestExcludedHistoryChatLeavesNoParticipantsOrMedia` asserting `participants` and `media` hold nothing from the excluded chat (include a media message in it).
5. Add `TestHistoryFromMeInLIDChatHasOwnLIDAsSender` (LID DM branch).
Report to h6b-report.md; reply with the short status contract.
