# Phase 0 fixes — final report (2026-10-15)

Branch `phase0-fixes`, pushed to the private origin at `5c5b40f` (186 commits above `main` at `6c3a6d9`). `main` is untouched. The full decision log, every deferred item and every ruling is in `ledger.md` next to this file.

## Verdict

Ready for Task 12 once you have read "Before you pair" below. Every finding in the "before Task 12" list of `docs/reviews/2026-10-14-phase0-review.md` is fixed with a red test first, reviewed, and covered by a mutation spec. A whole-branch review found one more data-loss path (history stubs) and two Important ones (alert text, shutdown mid-history); all three are fixed and re-reviewed.

## Gates at `5c5b40f`

| Gate | Result |
| --- | --- |
| `make check` (race tests, lint, 5-platform build) | exit 0, `0 issues` |
| Mutation run, all 285 specs (batched, scratch build cache) | 285 KILLED, 0 survived, 0 invalid, 0 no-match |
| Original review for comparison | 42 of 61 Review Focus mutations survived; 55 of 107 planned tests and 9 of 13 prior-art tests missing |
| Prior-art Table 1 tests | 13 of 13 exist |
| Gap list (115 planned names) | 113 found; the other 2 are merged or spelling variants |

Two review probe files (`ingest`, `media`) no longer compile against the new APIs and were not re-run. The behaviours they probed are covered by committed regression tests. The `cmd`, `backup` and `store` probes were re-run in a scratch copy and show the fixes: `status` no longer writes `archive.db`, a mistyped `--data` creates nothing, foreign backup folders are kept, and inserts stay flat at about 7000/s up to 30k rows (404/s before).

## What changed, by stage

- **Stage 0:** layout tidy; M1 tooling (`make check` works with no PATH prefix).
- **Stage 1:** B1/H2 write retry (a transient database error no longer drops an event); B2 reconnect back-off (no more immediate re-dial after `client_outdated`); H18 pairing exit codes; M3, M13.
- **Stage 2:** H5/H6/H6b/H9 history parsing, senders, wrapped messages, chunk ledger; H1 insert scaling and indexed alias lookups; H7/H8 edits, revokes, protocol messages.
- **Stage 3:** H10/M2 health alerts; media H11/H3/H4/M4 (panic accounting, valid re-upload requests, rate-limited and bounded re-uploads, seconds-scale back-off with later re-queue); backup H15/H16.
- **Guards:** H12 static CON-1 guard widened (prefix rule across `internal/wa`, interface and generic-constraint methods, embedding, alias, exported client types).
- **Ingest follow-ups:** exclusion filter fails closed on an alias read error; other secret-encrypted message types stored as `secret_encrypted` events.
- **CLI:** H17 `status`/`chats` read-only, in your time zone, with the planned fields and golden tests; H14 recorder for every kind, a Scrub that rewrites numbers inside protobufs, `scrub` and `replay` commands, `DumpRaw`, a synthetic golden.
- **Tests and lifecycle:** H13 gap tests plus goleak; M5 `serve` joins its loops and the write retry loop ends on shutdown.
- **Docs:** M8 setup guide and acceptance log (SQL tested against a replayed synthetic archive).
- **Final wave (from the whole-branch review):** history stub no longer aborts a chunk (C1); alert text carries no chat identifiers (I1); shutdown waits up to 30 s for an in-flight handler (I2); `exclude` entries validated at config load; Scrub clears snapshot bytes; `replay` exits non-zero when writes failed; docs corrections.

## Rulings I made on your behalf

The 18 rulings in `docs/prompts/2026-10-14-phase0-fixes-handover.md` still stand. New in this session, each with its cost if wrong:

1. **Media fix bound:** an expired row whose re-upload was already applied, or with 20 attempts used, becomes `failed` instead of being asked for again. Cost: a file that expires a second time is lost.
2. **H12 guard scope:** the prefix rule covers all of `internal/wa`. Cost: false positives inside `wa`, which fail safe.
3. **Accepted `immutable=1` for `status`** only when no `-wal`/`-shm` exists. Cost: a `status` racing a service start can print wrong output; the archive is never harmed.
4. **Scrub:** clears `payload`/`bytes`/`snapshot` byte fields; rewrites `@number`, `waid=` and vCard `TEL` values with the same stable fake; fakes are HMAC-SHA256 under a private key at `os.UserConfigDir()/archiver/scrub.key`. Cost: losing the key changes future fakes (committed fixtures stay valid).
5. **Handle context:** the serve context only stops the retry loop; each write runs under `context.WithoutCancel`. Cost: a hung database could delay shutdown, bounded by the 30 s drain and systemd's 90 s.
6. **Ack/redelivery (your "whatever you say"):** I chose NOT to enable `EnableDecryptedEventBuffer` or `AddEventHandlerWithSuccessStatus`. Cost: an event whose write is already failing at the moment of SIGTERM can be acked and lost.
7. **History stubs:** bodyless stubs (revoke, group-change and E2E markers) are skipped and logged, not archived. Cost: those markers are not in the archive; they could later be stored as `message_events`.
8. **Alert body:** fixed text, no error or JID. Cost: less detail in the push notification (the journal has it).
9. **Shutdown drain:** 30 s bound. Cost: a stop can take up to 30 s longer during an import, and exits 1 if cut.
10. **`exclude` validation:** a bad entry fails config load. Cost: an old config with a bad entry now fails to start instead of silently excluding nothing.

## Decisions only you can make

- **Ack/redelivery:** if you want zero loss on a SIGTERM during a failing write, enable `AddEventHandlerWithSuccessStatus` and `EnableDecryptedEventBuffer`. The buffer stores decrypted plaintext in `session.db`, which is never backed up. History-sync notifications are acked immediately in any case.
- **Key material (ruling 14):** four key-carrying protocol message types are stored without their raw bytes because `archive.db` is backed up. Confirm you accept that.
- **Recording and practice pairing:** `--record` and a practice pairing store excluded chats unscrubbed on the server until you delete them. The guide says so; decide whether to skip them.
- **Stub messages:** archive them as events? (ruling 7)
- **Open items worth scheduling:** the exported-`any` gap in the CON-1 guard (needs deliberate code inside `wa` to exploit), `go mod tidy`, goleak in `wa`/`backup`/`store`, backup opening `archive.db` read-write, the remaining docs minors M2 and M5–M14. All are listed in the ledger.

## Before you pair (Task 12 is yours)

- Follow the Setup guide in `README.md` in order. Never run `archiver run` in a terminal once the service is installed.
- Press Ctrl-C during the first history sync only after no `history chunk imported` line has appeared for several minutes.
- The Ubuntu, systemd, ntfy and phone-UI outputs in the guide were written from general knowledge and were not followed on a real host. Expect small wording differences.
- Two agents (an implementer and a reviewer) ran `archiver run` once on an empty scratch folder to capture the exit-3 output, against the instruction not to. It exits at a local file check before any network access, so nothing connected to WhatsApp or any other host.

## Build and copy to the Linux server

```sh
make build-all
ssh hamza@SERVER uname -m          # x86_64 -> amd64, aarch64 -> arm64
scp .cache/build/archiver-linux-amd64 hamza@SERVER:/tmp/archiver     # or archiver-linux-arm64
scp deploy/archiver.service hamza@SERVER:/tmp/archiver.service
sudo install -m 0755 /tmp/archiver /opt/whatsapp-archiver/archiver   # on the server
```

The guide's sections 3 and 12 cover the rest, including the `Exec format error` fix for a wrong architecture and the update steps.

## Cleanup you may want

- `archiver/.cache/gocache` is about 36 GB of Go build cache from the earlier mutation runs. It is safe to delete and will rebuild on the next `make check`.
- Worktrees `../wt-backup`, `../wt-h12`, `../wt-h13`, `../wt-h14`, `../wt-h17`, `../wt-ingest` and `../wt-m8` are merged and can be removed with `git worktree remove` once you no longer need them. Their `fix/*` branches were not pushed.
- `main` is one commit ahead of `origin/main` (the Stage 0 layout commit); I did not push it.
