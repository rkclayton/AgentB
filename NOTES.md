# Implementation notes

## Prompt 1 assumptions and deviations

- HOMEPC is the target serving machine. Work was performed remotely over Tailscale and Windows OpenSSH because the repository workspace is on another Windows machine.
- `E:\llama` and `E:\Models` replace the documented `C:` defaults because `C:` had insufficient room for the 13.1 GB model while `E:` had about 471 GB free.
- llama.cpp `b10775` was the newest official build release with Windows CUDA artifacts at installation time. The CUDA 13.3 bundle was selected because Blackwell requires CUDA 12.8 or newer; direct device discovery and inference verified compatibility with driver 591.86.
- The exact model download was `hf download unsloth/Qwen3.8-27B-GGUF Qwen3.8-27B-UD-Q3_K_XL.gguf --local-dir E:\Models\Qwen3.8-27B`. The MTP draft was downloaded separately only after baseline passed.
- Rung 1 was skipped because the RTX 5070 Ti drives the display. The required near-margin retry of rung 2 used `-ot token_embd.weight=CPU`; it did not restore the margin and is off by default.
- MTP improved decode by more than 20% and retained valid tool calls, but its 205 MiB remaining VRAM violated the safety margin. The files and switch remain available for later testing; the default is off.
- No prescribed flag was rejected. No context-shift, cache-reuse, chat-template default, reasoning-budget, or checkpoint flag was added.
- Ollama 0.23.2 was running, but `/api/tags`, `/api/ps`, `D:\AI\models\manifests`, and `D:\AI\models\blobs` were empty. Therefore its prior effective context is recorded as unknown.
- Anonymous Pollinations endpoints returned 401/402 and were rejected as a public-provider candidate. Ollama Cloud was reachable but returned 401 until the desktop app is signed in; no credential was stored.
- The shell account and SSH keys are operational access only. They are excluded by `.gitignore` and never appear in tracked files.

## Operational cautions

- `llama-server` intentionally has no API key because it binds only to `127.0.0.1`. Use SSH forwarding for remote API access; do not expose port 8080 directly to the LAN or tailnet without authentication.
- The shell tool built later is not a security boundary. The workspace must remain disposable, commands are logged, and OS-level isolation is deferred as specified in `INTERFACES.md`.

## Prompt 2

- The permanent reliability canary uses Go 1.27.1. The official portable Windows archive was installed under `E:\tools\go` on HOMEPC because the MSI path was blocked by local execution policy; no Python is used by the canary or its runner.
- C1 (`UD-Q3_K_XL`, 32K) and C2 (`UD-IQ4_XS`, 16K) each passed 6/6 trials. The prescribed tie rule retains C1. C2 loaded with `--fit off` and did not need partial offload.
- C2 was downloaded directly from `unsloth/Qwen3.8-27B-GGUF` with `curl` and is 14,252,845,984 bytes. The end-to-end runner wall time was 18.51 minutes, of which about 15.8 minutes was the 13.27 GiB download.
- The final C1 rerun passed canaries 1, 2, and 8 at 32,768 context: health HTTP 200, overflow HTTP 400, 1,729.75 prompt tok/s, and 50.27 generated tok/s.

## Prompt 3

- The harness foundation uses Go 1.27.1 with only the standard library. It binds to `127.0.0.1:8790`, uses `E:\AgentB\sandbox`, creates session `main`, masks API keys, and persists per-session/global JSONL.
- The full `local` probe found: `props` and 32,768 context; tokenizer and plain/tool template application; cached-token accounting and timings; streaming and prompt progress; native grammar-constrained tool calls; `chat_template_kwargs` reasoning with `low, medium, xhigh`; and overflow errors.
- Minimal mode was exercised and updated `probed_at` while explicitly marking generated-token checks as assumed. Full mode was restored. SSE delivered `snapshot`, `server.probed`, and session events.
- v1 migration was exercised with a Windows UTF-8 BOM: old keys were removed, the profile became `servers[local]`, profile context values and global thresholds were preserved, and the required migration log line was emitted.
- Session create/close, unreachable-profile probing, the `context length unknown` degradation reason, deep config merge, profile removal, and all prompt-3 501 stubs were verified on HOMEPC.
- Deferred exactly as scoped: agent loop, tools, scheduler, system-prompt template, full frontend, memory, compaction, and replay.

## Prompt 4

- The first live AgentB run called `list_dir` and `read_file` in that order, correctly summarized the README, streamed prompt-progress/content/tool deltas, and stopped `done` after two model turns. Its JSONL contains full `model.request.body`, raw `model.response`, all stage pairs, tool calls/results, messages, and budgets tagged `main`.
- With `max_concurrent=2`, `main` and the second session entered `call_model` concurrently while llama.cpp `--parallel 1` serialized their actual slot work; the third session queued at position 1 and began when a slot cleared.
- `stop {all:true}` cancelled two active and one queued run in 0.618 seconds; all three recorded `user_stop`, and a new message was immediately accepted.
- A one-turn tool task stopped at `turn_ceiling`; disabling `read_file` changed the next request from two schemas to one and removed its name from the rendered prompt; editing and reloading `prompts/system.md` changed the next request and was then reverted.
- A forced non-runnable profile produced `profile_not_runnable` without a model request. Session reset guards and the prompt-4 APIs were exercised on HOMEPC.
- Deferred as scoped: write/edit/grep/shell/remember, cycle and tool-error stopping, approvals, compaction/elision, exact accounting, memory injection, queued-message delivery, frontend, and replay.

## Prompt 5

- `web/DESIGN.md` is the verbatim design contract. IBM Plex Sans and Mono 5.3.0 latin 400/500 WOFF2 assets were downloaded from the official npm registry and are self-hosted.
- The dependency-free shell was visually verified in headless Chromium at 1440×900 through an SSH tunnel to HOMEPC. It rendered the header, active tab recess, context rail, computed eight-node flow, tool rack, composer, and empty State/Timeline wells without page scrolling.
- Two concurrent runs showed two Trace tab lamps. Switching tabs during `call model` retained each session's active stage and traveling dot; Stop remained available.
- With `tokenize=false` and `cached_tokens=false` temporarily applied, the rail rendered two estimated-category outlines, showed a `~` total and `context (estimated)`, and omitted the cached readout. The measured capabilities were restored.
- Static verification passed for every JS module. CSS contains exactly the six palette values, no uppercase transform, and only the inset recess shadow.
- Deferred as scoped: State and Timeline content, settings sheet, `/chat`, replay, memory UI, and any additional motion.

## Prompt 6

- Registered `read_file`, `list_dir`, `write_file`, `edit_file`, `grep`, and `shell` in the fixed prefix order. Approval remains `off`, queue depth remains `0`, cycle window remains `8`, and the consecutive tool-error limit remains `3`.
- The table-driven edit and jail suite passes all required cases on HOMEPC. Atomic replacement, BOM/EOL preservation, indentation recovery, stale-view notes, and cross-session conflict refusal are covered. A live A/B swarm refused A with `session B wrote this file`, published both session ids, then succeeded after A re-read the file.
- The live model repaired `buggy.go` through `read_file`, `edit_file`, and `shell`; `go run buggy.go` returned `CHECK_OK` and the run stopped `done`. Cached prompt tokens across its four turns were `0`, `1002`, `1106`, and `1260`.
- Repeating the same successful read published `cycle.detected` and stopped `cycle`. With the window set to `0`, the repeated reads continued to the two-turn ceiling. Three distinct failed reads stopped `tool_errors` with the last error in `detail`.
- `approval.mode=mutating` paused a live write, published `approval.required`, and resumed through `/api/approve`; mode was restored to `off`. With queue depth `2`, two messages queued in order, the first started automatically, and stopping it discarded the second with `discarded 1 queued message(s)`.
- A two-second shell timeout returned after 2.079 seconds and killed its spawned child; the child's delayed marker was still absent nine seconds later. `shell` is not a security boundary: OS-level sandboxing remains deferred, as do compaction, exact accounting, memory, settings, chat, and replay.

## Prompt 7

- The active-session State well now renders live and synthetic schema rows, exact/estimated token counts, all seven category filters, elision styling, expandable content, and collapsed reasoning details. The Timeline groups model calls with tool sub-rows and renders approval, conflict, message/run queue, compaction, and stop entries.
- Headless Chromium verified 16 mounted State rows (15 messages plus schemas), three model rows, two tool rows, Plex Mono expansion blocks, fixed body overflow, and expansion state surviving an A/main/A tab round trip. Every JavaScript module passes `node --check` and the page reported no script or resource failures beyond the ignored browser favicon request.
- A live conflict emitted while the page was connected appeared in both A and B timelines with the same path, writer label, session ids, and age. A live mutating call showed `paused`; clicking the Timeline's Approve control produced `approve`, resumed the run, and mode was restored to `off`.
- The earlier queue-depth exercise renders both `message.queued` ledger rows and the later runs in order. Cycle and tool-error stop rows retain Alarm state until the next run, paused dispatch holds Trace, and a conflict flashes the active tool lamp for 600 ms.
- Defaults were retained. Deferred as scoped: compaction production, exact accounting, memory, settings, `/chat`, and replay.
