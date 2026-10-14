# WhatsApp Archiver — Phase 0

A headless, receive-only WhatsApp capture logger. It stores immutable raw messages/events and media locally, tracks history coverage and decryption gaps, snapshots the archive, and reports health through ntfy.

```sh
make check
make build
.cache/build/archiver version
```

`make` bootstraps the pinned, checksum-verified Go toolchain and golangci-lint into `.tools/` on first use; nothing is installed globally.

Runtime configuration and databases belong under `data/` and are not committed. See `docs/phase0-setup.md` for Ubuntu deployment and `docs/phase0-acceptance.md` for the manual iPhone acceptance run. Do not run `pair` or `run` against a real account until performing that checklist deliberately.
