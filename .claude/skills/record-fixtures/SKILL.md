---
name: record-fixtures
description: Record and commit safe WhatsApp replay fixtures.
---
Record only around a throwaway test group. Never record real chats into `testdata/`; never connect to WhatsApp from tests or agent sessions (recording is a manual step on the server).

1. Record (manual, on the server): `archiver run --record ~/rec` (or `archiver pair --record ~/rec` at pairing; a later recorder on the same folder continues the numbering). Each supported event (message, undecryptable, history sync, group info, joined group, archive, mute, pin, media retry) becomes `~/rec/<seq>-<kind>.json`. Stop with Ctrl-C, then remove `--record`.
2. Keep only the test group: the recorder sees every chat on the account. Copy just the files whose `info` names the test group, e.g. `mkdir ~/rec-group && grep -l '"<test-group-jid>"' ~/rec/*.json | xargs -I{} cp {} ~/rec-group/`. Never keep `history_sync` files from a real account: a chunk carries every conversation (the synthetic scenario covers history).
3. Scrub: `archiver scrub ~/rec-group ~/rec-scrubbed`. Phone numbers, LIDs and group IDs (with any device or agent suffix, in the JSON and inside every protobuf string field) become stable fakes starting with `0`; keys, key data, secrets, tokens and sender keys are cleared; text is kept.
4. Review by eye: read every file in `~/rec-scrubbed` and replay it into a fresh folder to see what it stores: `archiver replay ~/rec-scrubbed --data /tmp/replay-check --dump`. Nothing identifying may remain (names in push names and group names are kept: the test group's content must be written for the purpose).
5. Commit the corpus: `cp -R ~/rec-scrubbed internal/ingest/testdata/events/<name>/`; read every line of the dump from step 4, log that review line by line in `PROGRESS.md`, then create the golden once: `go test ./internal/ingest -run TestReplayMatchesGolden -update` (through the Makefile toolchain) and check it equals the reviewed dump. `make check` must pass; `TestReplayMatchesGolden` replays every folder in `testdata/events/`.
6. Delete `~/rec`, `~/rec-group` and `~/rec-scrubbed`.

The synthetic scenario (`testdata/events/synthetic/`) is generated, not recorded: change `syntheticEvents()` in `internal/ingest/golden_test.go`, then `go test ./internal/ingest -run TestSyntheticEventsMatchGenerator -update`, review, and regenerate its golden as in step 5.
