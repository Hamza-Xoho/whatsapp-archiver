---
name: bump-whatsmeow
description: Safely update the pinned whatsmeow main pseudo-version.
---

1. Read the whatsmeow commit log from the current pin through `main`, focusing on API and protocol changes.
2. Run `go get go.mau.fi/whatsmeow@main` and `go mod tidy` with the repository-local Makefile environment.
3. Update `docs/prior-art.md` Table 2 whenever an identifier or signature moved.
4. Run `make check`, including the replay corpus and all cross-builds.
5. Build and deploy through the documented release process.
6. Confirm `archiver status` reports `connected` and the new whatsmeow/client versions.
