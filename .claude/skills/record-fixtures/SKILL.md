---
name: record-fixtures
description: Record and commit safe WhatsApp replay fixtures.
---
Record only a throwaway test group. Stop recording, scrub into a separate folder, inspect every changed line and protobuf-derived identifier, generate the golden once, record that review in `PROGRESS.md`, and commit only the scrubbed corpus. Never record real chats into `testdata/`.
