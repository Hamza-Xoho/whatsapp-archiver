# Brief: H18 (unpaired run) + M13(a,b) (pair feedback) + B2 review carry-overs

Repo /Users/hamza/Code/whatsapp-archiver/archiver, branch phase0-fixes. Files: `cmd/archiver/main.go`, `cmd/archiver/main_test.go`, `internal/wa/wa.go`, `internal/wa/reconnect.go`, `deploy/archiver.service`, `internal/ingest/pipeline.go` (one case only), `docs/phase0-setup.md` (one line only, if needed).

## H18 (verbatim)
`run` only checks that `session.db` exists. `pair` creates that file on start (before any scan), and a logout keeps it. `run` then fails with the bare message `not paired`, exits 1, and `Restart=always` retries every 10 s forever with no alert and no "run `archiver pair`" hint. This is the state after any logout followed by a restart (RF4 "stays up reporting logged_out instead of crash-looping"). (`main.go:57`; `wa/wa.go:94-96`; `deploy/archiver.service:11`.) Probe: `TestProbeRunWithEmptySessionFile`: `exit=1 stderr="not paired"`.
Fix (review): ask the client whether the device is paired; print the hint; use `RestartPreventExitStatus` for that exit code; send a one-off ntfy. Red test (name fixed): `TestRunWithUnpairedSessionFileSaysPairFirst`.

## M13 (a, b) (verbatim)
(a) The wa wrapper never wires `PairError` or QR-timeout feedback: if pairing fails the user sees nothing. (b) `pair` prints "Paired. Capture is running." after `Run` returns, i.e. when you press Ctrl-C, even if pairing never succeeded. Fix: print on `events.PairSuccess` (and print the error on `events.PairError`; print a line when the QR channel reports `timeout`/error items — look up `QRChannelItem` events in the pinned source `qrchan.go`).

## B2 review carry-overs (from the previous task's review; you wrote that code)
- `TestLoggedOutStopsTheReconnectLoop` must prove the loop keeps blocking after stop (bounded window or a "parked" signal), so a mutant that returns immediately on stop is killed.
- Extract the handler's control-event forwarding into a small func and table-test it (every forwarded type, plus a non-control event not forwarded). Test `sleepCtx` cancel path.
- Loop-level test with refresh returning changed=false (30-minute wait through the loop).
- Unknown connect failures: whatsmeow dispatches `*events.ConnectFailure` (only for reasons it neither auto-reconnects nor maps to LoggedOut/TemporaryBan/ClientOutdated, `connectionevents.go:150-154`) and `*events.CATRefreshError`, and leaves the socket down → today the process idles forever. Ruling: forward both; policy → disconnect, wait 30 minutes, connect (fixed, no doubling). In `ingest.Pipeline.Handle`, map `*events.ConnectFailure` to `h.SetState("disconnected", "<reason> <message>")` so the later disconnected-too-long alert (H10) covers it (test it in `internal/ingest`).

## Controller rulings
1. Paired check happens before any network access: wa exposes a function that opens session.db via `sqlstore`, loads the first device and reports whether `Store.ID != nil` (a missing session.db = unpaired; don't create it as a side effect if avoidable — if sqlstore must create it, say so). `run` calls it first; `wa.Open`'s version lookup (`GetLatestVersion`, network!) must not run for an unpaired device. Tests must never touch the network: set `check_latest_version: false` in test configs anyway.
2. Exit code for "not paired": a named constant, value 3. stderr: `not paired: run \`archiver pair\` in a terminal first` (keep wording close to that). `deploy/archiver.service` gets `RestartPreventExitStatus=3`. A test reads the unit file and asserts the line exists and matches the constant (cheap guard).
3. One-off ntfy: if `alerts.ntfy` is configured, send one notification ("WhatsApp Archiver is not paired" / hint) via the existing `health.NewNtfy`/`Notifier` before exiting; failure to send is logged, not fatal. Test it with an `httptest.Server` as the ntfy URL (external boundary). Look at `internal/config` for the ntfy config shape.
4. `pair` on an already-paired device keeps its current error. `pair` prints "Paired. Capture is running." on `PairSuccess` (from inside the event path), not after `Run` returns. Testable via a small function that maps pair/QR events to output lines — keep it tiny.
5. Least code; no dead code; errors wrapped.

## Process
Red tests (`test:` commit, `_test.go` only; compile-only red for new API is acceptable if you show behavioural failure against a scratch stub) → `touch .claude/green.lock` → implement → `make check` → `rm .claude/green.lock` → `fix:` commit. Mutations appended to `mutations/specs.json` and run with `mutations/run.py`, all KILLED: x10 (the "pair first" message removed), h18-exit (exit code changed back to 1), h18-paircheck (pairing check replaced by file-exists), h18-ntfy (no notification sent), m13-early ("Paired" printed after Run returns again), b2-logout-returns (stop returns immediately), b2-forward-ban (TemporaryBan dropped from forwarding), cf-idle (ConnectFailure not forwarded). Re-run probe `TestProbeRunWithEmptySessionFile` in a scratch copy and show the new output. PROGRESS.md entry (`docs:` commit).
Hard rules: never connect to WhatsApp; never run the real binary's pair/run against anything; tests use temp dirs. No Co-Authored-By. No push.
