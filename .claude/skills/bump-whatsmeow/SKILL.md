---
name: bump-whatsmeow
description: Safely update the pinned whatsmeow main pseudo-version.
---

1. Read the whatsmeow commit log from the current pin through `main`, focusing on API and protocol changes.
2. Run `go get go.mau.fi/whatsmeow@main` and `go mod tidy` with the repository-local Makefile environment.
3. Update `docs/prior-art.md` Table 2 whenever an identifier or signature moved.
4. Re-diff the bytes fields of the new protos (`waE2E`, `waWeb`, `waHistorySync`, `waSyncAction`) against `secretFieldWords` and `secretFields` in `internal/ingest/secrets.go`: a new field that holds a secret, token, salt or key (not a message's media key) must match a word or be added, with a test in `internal/ingest/spoolstrip_test.go`. Unknown fields and opaque bytes (`futureproofData`) are not covered by the rule.
5. Run `make check`, including the replay corpus and all cross-builds.
6. Build and deploy through the documented release process.
7. Confirm `archiver status` reports `connected` and the new whatsmeow/client versions.
