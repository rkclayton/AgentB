# Agent_b repository guidance

## Orientation
- `NOTES.md` is the document of record for current state: decisions, discovery findings, and the follow-up card backlog. Read it first.
- `INTERFACES.md` and `SECURITY.md` are binding. `docs/HARDENING.md` is the operator runbook.
- `harness-plan.md` and the numbered prompt files `01-` through `10-` are the original build plan. **Historical reference only** — the project has moved well past them. They do not override `NOTES.md`, `INTERFACES.md`, or the current prompt.
- Numbered files `11-` and above are **current planned work**, not history. Execute them in order when directed.

## Do not lose NOTES.md
- Never delete, move, rename, truncate, or overwrite `NOTES.md`. It is gitignored, so git holds no copy and any loss is permanent.
- Append new prompt sections. Never rewrite existing ones.
- It has already been lost once in a refactor and was reconstructed incompletely. If a task appears to require removing it, stop and report instead.

## Implementation constraints
- Go 1.24+. Standard library by default; third-party dependencies require an explicit decision recorded in `NOTES.md`. Currently approved: a TLS-fingerprinting HTTP client for the `fetch_url` tool. One process, one binary. Browser code dependency-free.
- Preserve the contracts in `INTERFACES.md`. Keep tool registration order stable: `read_file`, `list_dir`, `write_file`, `edit_file`, `search_text`, `shell`, `remember`, `recall`, `fetch_url`, `find_files`.
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
