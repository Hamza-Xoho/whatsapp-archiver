---
name: add-event-kind
description: Add one captured WhatsApp event kind without bypassing the ingest path.
---

1. Add a failing normalizer test with a constructed or scrubbed fixture.
2. Add the `Kind` constant and its pure normalization.
3. Decide the explicit scope rule and raw store kind.
4. Feed it only through `Pipeline.Ingest`; do not add a second writer.
5. Add a scrubbed replay fixture and golden row.
6. If prior art exposed the case, add it to `docs/prior-art.md` Table 1.
7. Run `make check`.
