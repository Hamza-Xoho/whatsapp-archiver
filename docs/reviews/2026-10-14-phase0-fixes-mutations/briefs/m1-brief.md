# Brief: finding M1 — tooling so `make check` works on this Mac with no manual PATH

Repo: /Users/hamza/Code/whatsapp-archiver/archiver (branch phase0-fixes). Go module, Makefile at root.
Machine: macOS arm64, Apple GNU Make 3.81. Go 1.26.9 darwin-arm64 already in `.tools/go` (checksum-verified earlier).
`.tools/bin/golangci-lint` is now darwin-arm64 v2.14.0; `.tools/bin/golangci-lint.linux-amd64.orig` is a stray Linux binary (delete it — .tools is gitignored).

## Finding M1 (verbatim from docs/reviews/2026-10-14-phase0-review.md)
(a) Make 3.81 ignores the exported `PATH` for simple recipes, so the ".tools/go" bootstrap path never works on macOS: `make test` fails with `make: go: No such file or directory` even after Go is in `.tools/go`. Recipes with a shell metacharacter do find it.
(b) Nothing bootstraps Go on a fresh machine; the guide does not mention it.
(c) The `tools` target accepts a wrong-architecture binary (only checks the file exists).
(d) `curl … | sh` without a checksum.
(e) Cross-build covers darwin/amd64 only (this Mac is arm64); the Linux server's architecture is assumed amd64.
(f) `2>/dev/null || …` hides cross-build errors.
Fix suggested: set PATH so it works in Make 3.81 (e.g. invoke tools by absolute path variables, `GO := $(ROOT)/.tools/go/bin/go` falling back to `go` on PATH only if that file is missing); add a `go` bootstrap target with a checksum; check the lint binary actually runs (`--version`) and reinstall if not; build darwin/arm64 and linux/arm64 too; drop the fallbacks.

## Requirements
1. `make check` (= test with -race, lint, build-all) passes on this Mac when invoked as plain `make check`, no PATH prefix. Also `make test`, `make lint`, `make build`.
2. Bootstrap: a target (e.g. `.tools/go/bin/go`) that downloads the pinned Go version for the host GOOS/GOARCH from https://go.dev/dl/ and verifies its SHA-256 against a value pinned in the Makefile (or a small checked-in checksum file) before extracting into `.tools/go`. Pin the version in one place. Get real checksums from https://go.dev/dl/?mode=json&include=all for darwin-arm64, darwin-amd64, linux-amd64, linux-arm64. Use `shasum -a 256` on macOS / `sha256sum` on Linux (pick whichever exists). Only used when `.tools/go/bin/go` is missing; if a system `go` ≥ the module's go version exists you may use it, but keep it simple.
3. golangci-lint v2.14.0: download the release tarball for host OS/arch from GitHub releases (not `install.sh | sh`), verify against the pinned SHA-256 (fetch real values from the release's checksums.txt), install to `.tools/bin/golangci-lint`. The target must reinstall if the existing binary fails `golangci-lint --version` (wrong arch).
4. `build-all`: linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64 — each producing a named binary in `.cache/build/`, no `2>/dev/null ||` fallbacks; any compile error fails the target.
5. Keep GOPATH/GOMODCACHE/GOCACHE/GOBIN inside the repo as now. Install nothing globally. GOTOOLCHAIN: keep `auto` only if needed; prefer `local` so the pinned toolchain is used (check go.mod `go 1.26.0` vs installed 1.26.9 — fine with local).
6. Update the CI workflows in `.github/workflows/` only as far as needed to stay consistent (they currently set PATH manually; they may keep working as is — do not churn them unnecessarily).
7. Update `docs/phase0-setup.md` / `README.md` only with a one-or-two line note that `make check` bootstraps Go and lint into `.tools/` (no other doc rewrites — M8 is later).
8. Keep the Makefile short and readable. No dead targets.

## Hard rules
- Never run `archiver pair` / `archiver run` or connect to WhatsApp.
- No `_test.go` changes (this is tooling, not TDD-testable in Go). Verify by running: `make check` from a clean shell with `env -i HOME=$HOME PATH=/usr/bin:/bin:/usr/sbin:/sbin make check`; `file .cache/build/*` showing all 5 architectures; prove (c) by temporarily copying a wrong-arch file over the lint binary in a scratch way (e.g. `cp .tools/bin/golangci-lint /tmp-scratch backup; echo garbage>…`) and showing `make lint` reinstalls; prove (f) by showing a compile error surfaces (describe how, do not commit the breakage). Prove the Go bootstrap by running it into a scratch dir or by moving `.tools/go` aside temporarily (restore it afterwards!) — downloading ~70MB is acceptable.
- Commit as `fix: make check works on macOS Make 3.81 with pinned, checksummed tools` (one commit, or two if CI changes separately). NO Co-Authored-By trailer. Do not push.
