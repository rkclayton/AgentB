# Agent_b repository guidance

## Orientation
- Read `PLAN.md` at session start. `PLAN.md` is the sequencing authority. Read `NOTES.md` only by a targeted pointer (named heading, search result, or tail), never at session start and never from top to bottom; it is the append-only document of record for decisions, discovery findings, and the follow-up card backlog.
- `INTERFACES.md` and `SECURITY.md` are binding. `docs/HARDENING.md` is the operator runbook.
- Planning documents are not tracked. `PLAN.md` is gitignored and operator-owned; record durable findings and decisions in `NOTES.md`.

## PLAN.md is operator-owned
- Never create, delete, or commit `PLAN.md`. It is edited by the operator, outside your session, except that Codex may append progress lines beneath its existing `## In flight` heading while a work order is underway.
- Read it at session start for sequencing. After that, the work order you were given is the authority for the task in flight.
- `PLAN.md` appearing, changing, or being reordered mid-task is expected and is not a finding. Do not report it as a workspace anomaly, do not investigate it, and do not re-plan the task in flight because of it.
- If the operator wants a change to affect work already underway, they will say so in the session. Otherwise a `PLAN.md` change affects the NEXT task, not the current one.

## INBOX.md is the in-session channel
- At the start of every W item, before every commit, and before every hard-stop report, check only the byte size of `INBOX.md`.
- If it is non-empty, read it and act on the operator's instruction: `STOP` means finish the file in hand safely, report, and end; `REVISE: <text>` means re-read `## Current work order` and continue under the revision; any other text is a note to acknowledge.
- Append one acknowledgement line to `NOTES.md`, then truncate `INBOX.md` to zero so the flag resets. Only the operator or Fable writes new mailbox content; Codex never writes instructions there.

## Alpha testing instance — do not touch
- A second, production-shaped install runs on **port 7337**, with roots under `C:\alpha` and a git worktree at `C:\alpha\src`. It is the operator's daily driver while development continues.
- Do not bind port 7337. Do not read or write anything under `C:\alpha`. Do not stop, restart, or reinstall that process.
- Do not treat alpha's logs as test data or as differential-harness input. Its sessions are the operator's real work, not fixtures.
- Alpha shares the service account and host firewall rule with production. Do not create a second set, and treat any change touching them as affecting both.
- Alpha is deployed by the operator from a chosen commit. Its state is not evidence about `origin/main`.

## Do not lose NOTES.md
- Never delete, move, rename, truncate, or overwrite `NOTES.md`. It is gitignored, so git holds no copy and any loss is permanent.
- Append new prompt sections. Never rewrite existing ones.
- It has already been lost once in a refactor and was reconstructed incompletely. If a task appears to require removing it, stop and report instead.

## Implementation constraints
- Go 1.24+. Standard library by default; third-party dependencies require an explicit decision recorded in `NOTES.md`. Currently approved: a TLS-fingerprinting HTTP client for the `fetch_url` tool. One process, one binary. Browser code dependency-free.
- Preserve the contracts in `INTERFACES.md`. Keep tool registration order stable: `read_file`, `list_dir`, `write_file`, `edit_file`, `search_text`, `shell`, `remember`, `recall`, `fetch_url`, `find_files`, `run_script`, `call_service`.
- Keep model-dependent behavior in server profiles. Degrade honestly from probed capabilities. Never allow silent prompt truncation.
- Follow the six-color industrial-console design system; avoid cards, decorative motion, extra colors, and unsupported readouts.

## Security posture
- The OS is the boundary: separate low-privilege service account, NTFS ACLs, user-scoped outbound firewall rule. Tool-layer guards (`file_routing_guard`, workspace pinning) are ergonomics, not containment — do not describe them as security.
- Operator mode ("run tools as me") deliberately defeats that boundary while enabled. Treat any change touching it as security-relevant.
- Never weaken a guard or widen a reachable path without recording what changed and why in `NOTES.md` and, if it affects the posture, `SECURITY.md`.
- Machine state (Windows accounts, ACLs, firewall rules) is operator-run. Write scripts with `-WhatIf`; do not apply them yourself unless the prompt explicitly says to.

## Working method
- Inspect before editing. Make small exact changes.
- Run the prompt's verification plus relevant Go tests before declaring a phase complete.
- Report what was verified and what was not. Never claim a path works if it was never executed.
- Focused commits at verified milestones, pushed to `origin/main`. Never commit keys, model binaries, generated logs, or machine-specific secrets.
