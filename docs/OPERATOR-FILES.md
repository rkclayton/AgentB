# Operator files

Agent_b's operator data root is a plain folder. The files are intended to be readable, editable, searchable, syncable, and versionable with ordinary tools. Sync and source control remain the operator's responsibility; Agent_b does not listen on a network endpoint or push notifications for these files.

The active convention:

- `PLAN.md` — queued work as unchecked list items.
- `NOTES.md` — durable operator notes.
- `INBOX.md` — operator to Agent_b, one verb and payload, capped at 40 lines / 4 KB.
- `OUTBOX.md` — Agent_b to operator, timestamped human sentences, rotated at 200 lines.
- `STATE.md` — current run state, atomically replaced at each model turn.
- `attachments\` — files offered by Chat's paperclip menu.
- `chats\<dir-key>\` — closed-chat Markdown exports.

`INBOX.md` is read at run start, before each model turn, and while an approval card is waiting. `STOP` stops at the next boundary. `REVISE: …` adds the revision to the current chat. `TASK: …` appends a checkbox item to `PLAN.md`. `DELAY: 5m` delays the next turn, with a 24-hour maximum. Any other text is acknowledged and added to the current chat as a note. A pending approval can be answered with `ANSWER: once`, `ANSWER: session`, or `ANSWER: deny`; a repetition card accepts `ANSWER: continue` or `ANSWER: stop`. Mailbox answers work only when **Allow approvals from the mailbox** is enabled in Settings, and it is off by default. Mailbox answers cannot enable process-global operator mode or select the hidden run scope.

Whoever can write the operator folder can stop Agent_b or give it work. If mailbox approvals are enabled, that writer can also grant the agent the operator's identity. Treat the synced folder's security as the agent's security. Two private-sync options requiring no Agent_b integration are Cryptomator, using an encrypted folder mounted as a drive, and Obsidian Sync, using an end-to-end encrypted vault.

`OUTBOX.md` is pull-only. Its event vocabulary is `pause`, `done`, `stopped`, `needs you`, and `ready to test`. `STATE.md` is a status page, not a command channel. Evidence archives are excluded from log retention.
