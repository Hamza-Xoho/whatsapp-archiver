Evidence for `../2026-10-14-phase0-review.md`.

- `probes/` — throw-away tests (stored as `*.go.txt` so `go test ./...` and lint ignore them; rename to `.go` in a *scratch copy* to run) that print what the code actually does. They only log; none asserts. To run one, copy it next to the package it names in a *scratch copy* of `archiver/` (the ingest probes reuse helpers from the existing `*_test.go` files in that package), then `go test -run TestProbe -v ./internal/<pkg>/`.
- `harness/` — the mutation harness (`mutate.py`, `mut.sh`, `env.sh`). `mut.sh` copies a scratch base repo, `mutate.py` applies one exact-text mutation and runs `go test ./...`. Paths inside are absolute to the reviewer's session scratchpad; adjust `SP` before reuse.
- Nothing here is part of the product and none of it was run against WhatsApp.
