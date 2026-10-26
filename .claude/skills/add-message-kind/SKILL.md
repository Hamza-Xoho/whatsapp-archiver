---
name: add-message-kind
description: Add one message kind (a value of messages.kind with its details shape) or one message-event kind to the V1 normaliser without bypassing the ingest path.
---

Replaces `add-event-kind`. A message kind is a row of `messages` (`docs/v1/schema.md` section 4.2); an event kind is a row of `message_events`. Both go only through `Pipeline.Ingest` (live, history and replay share it), through `store.Apply`. Strict red, green, refactor (AGENTS.md): a `test:` commit first.

## A new message kind

1. Look up every whatsmeow identifier you will read (proto getters, event fields) in `docs/prior-art.md` and add a Table 2 row for each new one before using it. Prior art that exposed an edge case goes in Table 1.
2. Red: add a row to `TestEveryKindFillsKindTextCaptionAndDetails` in `internal/ingest/kinds_test.go` (kind, text, caption and the exact `details` JSON), a media row case in `TestMediaFactsKnownAtEnqueueGoOnTheMediaRow` when the kind has a file, and a context case in `internal/ingest/context_test.go` when it carries a context. `TestEveryStoredKindHasAMapping` fails until the normaliser produces the kind.
3. Add a scenario `kind-<name>` to `kindScenarios()` in `internal/ingest/golden_kinds_test.go` (include edge cases: Arabic with diacritics and tatweel, RTL mentions, quote, forward, ephemeral, view-once where they apply). It must be listed in `TestEveryKindHasAGoldenScenario`. Commit the red tests.
4. Green, `touch .claude/green.lock`:
   - `internal/store/kinds.go`: add the name to `MessageKinds`, a details struct in the field order of the schema table (numbers whose zero is real are pointers, everything absent is omitted) and its case in `detailsFor`. Update the table in `docs/v1/schema.md` 4.2.
   - `internal/ingest/content.go`: add the proto to one of the group functions (`textContent`, `fileContent`, `placeContent`, `contactContent`, `structuredContent`, `systemContent`) or a new group. Set `Kind`, `Text`, `Caption` and `Details` (`jsonOf` of the struct) and return the proto message (its leaf), so the context (quote, mentions, forwarding, expiry) and the view-once flag are read. A file kind calls `setMedia` with the facts known at enqueue (mime, size, filename, duration, width, height, pages, thumbnail); the file itself is downloaded later from `messages.raw_proto`.
   - A kind with a file: add it to `mediaKinds` in `internal/web/viewer.go`, to the `CASE` list in `internal/store/browse.go`, and to the type mapping in `internal/media/rules.go` if a rule must treat it as another type.
   - Keys never go into the archive: message secrets are stripped in `normalize.go`; a new key-bearing protocol type goes in `keyMaterial`.
5. Generate the golden once: `go test ./internal/ingest -run 'TestKindEventsMatchGenerator|TestReplayMatchesGolden' -update` (through the Makefile toolchain env). Read every line of the new dump against the generator input, then log the review in `docs/v1/STATE.md` (AGENTS.md). Commit it as `test:`.
6. Later pieces that read the kind: the viewer rendering of the bubble (`internal/web`, task f) and the FTS body (task d: text + caption + the kind's strings, such as poll options, location name and address, contact names, document filename). Note them in `docs/v1/STATE.md` if they are not done in the same change.
7. `make check` in full, remove `.claude/green.lock`.

## A new event kind (message_events)

1. Prior-art lookup as above.
2. Add the name to `EventKinds` (and a cause to `EventCauses` for `undecryptable`) in `internal/store/kinds.go`; values are validated in Go, never by a CHECK (R6).
3. Add the `Kind` constant in `internal/ingest/types.go`; decide in `scope.go` whether it is ever archived.
4. Normalise it in `normalizeEvent` (`internal/ingest/events.go`) through `event()`: the event's own message ID is `source_id` (R11, empty only for `undecryptable`); give a reaction-like event an event ID derived from that ID so a live and a history copy are one row; use `value` for its payload and `new_text` only for edit text.
5. An event embedded in a history message goes through `embeddedEvents` (R14); a payload that needs the session (like a poll vote) is decrypted in `internal/wa` and passed in as a function, never by importing the client in `ingest`.
6. Add a scenario to `events-messages` (or its own) in `golden_kinds_test.go`, tests for `source_id` in `events_test.go`, then the golden steps 5 and 7 above.
