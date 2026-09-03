# AgentB repository guidance

- Treat `harness-plan.md` as the document of record. Execute the revised prompt set in numeric order: `01-verify-serving.md`, `02-reliability-canary.md`, then `03-foundation.md` through `10-chat-replay-polish.md`.
- Older overlapping prompt files are reference material; do not let them override the revised plan or canonical prompts.
- Keep the implementation Go 1.24+ with the standard library only, as one process and one binary. Keep browser code dependency-free.
- Preserve the contracts in `INTERFACES.md` once created. Keep tool registration order stable: `read_file`, `list_dir`, `write_file`, `edit_file`, `grep`, `shell`, `remember`.
- Inspect before editing, make small exact changes, and run the prompt's verification plus relevant Go tests before declaring a phase complete.
- Keep model-dependent behavior in server profiles and degrade honestly from probed capabilities. Never allow silent prompt truncation.
- Follow the six-color industrial-console design system in the plan; avoid cards, decorative motion, extra colors, and unsupported readouts.
- Make focused commits at verified milestones and push them to `origin/main`. Never commit keys, local model binaries, generated logs, or machine-specific secrets.
