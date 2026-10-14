# Phase 0 manual acceptance — iPhone

Task 12 is intentionally not run by Codex. Fill every result before phase 1.

- [ ] Record iOS version: ____; WhatsApp version: ____; date/time: ____.
- [ ] Configure exclusions, timezone and ntfy; test alert received: ____.
- [ ] Pair once with `--record`; keep running 10+ minutes; pairing time: ____.
- [ ] Test group covers text, reply, forward, edit, revoke, reaction, pin, poll/vote, voice, PDF, image, sticker, >100 MB video, group name, participant add/remove, archive and mute. Result: ____.
- [ ] Receipt audit with every other linked device closed, phone offline 30 minutes, then 24-hour observation: no extra ticks, online or typing. Result: ____.
- [ ] Retired-version drill (`2.3000.1040847988`, latest check off) reports `client_outdated`, alerts, stays up; clearing override reconnects. Result: ____.
- [ ] Restart mid-conversation has no miss/duplicate; 20-minute outage alerts and recovers. Result: ____.
- [ ] Unlink alerts; fresh pairing succeeds. Result: ____.
- [ ] LID dedup raw duplicate count: ____; `messages_resolved` duplicate count (must be 0): ____.
- [ ] At 48 hours record oldest message/progress/chunks/late chunks per BMC group and chat type: ____.
- [ ] Scrub test-group recording, inspect every line, add event corpus/goldens, remove recorder, delete original: ____.
- [ ] Restore latest archive snapshot elsewhere; counts match and no session exists: ____.
- [ ] Seven-day audit of two groups: zero missing; every gap recovered or explained. Result: ____.

Decision forced by any spike: ____.
