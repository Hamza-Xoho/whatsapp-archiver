# Brief: ingest follow-ups from reviews (two red/green cycles)

Repo /Users/hamza/Code/whatsapp-archiver/archiver, branch phase0-fixes. Read: `AGENTS.md`; `internal/ingest/scope.go`, `pipeline.go` (`Ingest`, `persist`), `normalize.go`, `chatevents.go`; `internal/store/store.go` (`Aliases`, `aliasSet`); `cmd/archiver/main.go` (aliases closure `v, _ := st.Aliases(...)`). Same process as before: red `test:` commit → `touch .claude/green.lock` → least code → `make check` → `rm .claude/green.lock` → `fix:` commit → mutations appended to `mutations/specs.json`, full run all KILLED → `docs:` PROGRESS entry. No Co-Authored-By, no push, never connect to WhatsApp, never weaken tests.

## Cycle A — exclusion filter fails closed (privacy)
Finding (H1 review): `cmd/archiver/main.go` discards the error from `Aliases` (`v, _ :=`). On a read error the filter gets nil aliases and keeps the event, so the exclusion filter fails OPEN: an excluded chat seen under its other alias would be stored. `Aliases` also returns unwrapped errors.
Ruling: the aliases func returns `([]string, error)`; `Filter.Keep` returns `(bool, error)`; `Ingest` (and `ImportHistory`'s chat gate) evaluate the filter inside `persist`, so a read error retries until it succeeds (never leaks an excluded chat, never drops a kept one) and alerts once via the existing WriteFailed path (or a sibling — least code; name what you chose). `Aliases` wraps errors with context. Mapping writes stay before the filter (RF8).
Red tests: `TestAliasLookupErrorNeverLeaksExcludedChat` (aliases fails N times then returns the excluded alias → nothing stored, retried, one alert), `TestAliasesUnionForPhoneWithSeveralLIDs` (store: P→L1, P→L2 → Aliases(P) = [L1, L2], pins the H1 behaviour). Mutations: `ff-open` (error treated as "no aliases"), `h1-union` (Aliases returns a single canonical).

## Cycle B — other secret-encrypted messages
Finding (H7 fix round): `SecretEncryptedMessage` types other than MESSAGE_EDIT (POLL_EDIT, EVENT_EDIT, POLL_ADD_OPTION, MESSAGE_SCHEDULE and any others in the pinned enum — look them up in `.cache/gomod/go.mau.fi/whatsmeow@v0.0.0-20261013142727-4cf8bc8e0aeb/proto/waE2E/`) are stored as empty message rows.
Ruling: store them as a message event of kind `secret_encrypted`, `Value` = the SecretEncType name, target = `TargetMessageKey.ID`, raw kept, no message row (mirror the MESSAGE_EDIT/poll-vote path; reuse, don't copy). Red test: `TestSecretEncryptedNonEditIsAnEventNotAMessage` (table over the types). Mutation `se-asmessage`.

Report to `.superpowers/sdd/claude-code-phase0-fixes/ingest-followup-report.md`; reply with the short status contract.
