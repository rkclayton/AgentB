# Implementation notes

## Prompt 12 — repo audit

Audit performed before changing the worktree. This is a Git repository rooted at `C:\projects\AgentB`. It has remote `origin` at `https://github.com/rkclayton/AgentB.git`, and history has been pushed: local `main`, `origin/main`, and the remote-advertised `refs/heads/main` all pointed to `6f92ba3247f2eecafbae48ff899e07d6f21830e1` at audit time.

The following requested paths have been committed and pushed:

- `harness.json`
  - added by `7a0ea34203bc497f03980e7c5f13fb49f2c9b8d8` (`feat: establish harness foundation`)
  - modified by `d2165ea8549971a0776b36b29faa3633ebc3a223` (`docs: record verified harness foundation`)
  - both historical versions have `servers[local].api_key` present but empty. The config format stores a nonempty API key as plaintext on disk even though API responses mask it; this audit found no nonempty key in the committed `harness.json` versions.
- Reliability-canary implementation fixture JSONLs, added by `5bf91153206527c96532be20cd4c65a056cff912` (`feat: add model reliability canary`):
  - `serve/probes/reliability/testdata/score/fail.jsonl`
  - `serve/probes/reliability/testdata/score/pass.jsonl`
- Reliability-canary run directory, added by `86a01ab029d69347d76b5a22633824fd0757dce5` (`test: qualify model reliability verdict`):
  - `serve/probes/reliability/runs/c1/add-1.jsonl`
  - `serve/probes/reliability/runs/c1/add-2.jsonl`
  - `serve/probes/reliability/runs/c1/add-3.jsonl`
  - `serve/probes/reliability/runs/c1/fix-1.jsonl`
  - `serve/probes/reliability/runs/c1/fix-2.jsonl`
  - `serve/probes/reliability/runs/c1/fix-3.jsonl`
  - `serve/probes/reliability/runs/c1/score.md`
  - `serve/probes/reliability/runs/c2/add-1.jsonl`
  - `serve/probes/reliability/runs/c2/add-2.jsonl`
  - `serve/probes/reliability/runs/c2/add-3.jsonl`
  - `serve/probes/reliability/runs/c2/fix-1.jsonl`
  - `serve/probes/reliability/runs/c2/fix-2.jsonl`
  - `serve/probes/reliability/runs/c2/fix-3.jsonl`
  - `serve/probes/reliability/runs/c2/score.md`
  - `serve/probes/reliability/runs/summary.json`
- `serve/probes/samples/stream-toolcall.txt`, added by `e26284c7382fb9efc3780debcbeeea04e1b16750` (`feat: verify and pin local model serving`).
- `SERVING.md`
  - added by `e26284c7382fb9efc3780debcbeeea04e1b16750`
  - modified by `86a01ab029d69347d76b5a22633824fd0757dce5`
  - modified by `bb8c4f4cc72936a308945472935b53ab532f90eb` (`docs: verify HOMEPC tailnet inference`)
- `NOTES.md`
  - added by `e26284c7382fb9efc3780debcbeeea04e1b16750`
  - modified by `86a01ab029d69347d76b5a22633824fd0757dce5`
  - modified by `d2165ea8549971a0776b36b29faa3633ebc3a223`
  - modified by `bf63b81222e0fd9fd2b057b8a6064e3a91d55e59` (`docs: record live agent loop verification`)
  - modified by `18490efbc7897d00ec8ecdb7468bfb1666de1101` (`docs: record frontend verification`)
  - modified by `6e7ba4fc37d7be87b4694379b584e9876dc1ca81` (`docs: record guarded tool verification`)
  - modified by `f67352f72f12ac2e1609cdf24656cc0113e8c7be` (`docs: record panel verification`)
  - modified by `35a046bca8315638ff6285b98d0439ef07b872f8` (`docs: record accounting and memory verification`)
  - modified by `d68b5111086a7a79059da67cc9de50bb9105f006` (`docs: record settings verification`)
  - modified by `296d7bd4ad71ab425f4f59941c9968a0a3c02f6a` (`docs: finish AgentB operating guide`)
  - modified by `bb8c4f4cc72936a308945472935b53ab532f90eb`

No path under `logs/` or `memory/` has ever been committed on any reachable ref. Therefore the live `logs/*.jsonl` files containing verbatim model requests/responses, file reads, and shell results are currently untracked rather than present in Git history. The committed reliability-run JSONLs do contain the canary's request bodies and tool results, and `.gitignore` cannot remove those existing blobs from history.

Because listed JSONLs and `harness.json` are already in pushed history, removing them from all history requires either (1) a coordinated `git filter-repo` rewrite followed by force-pushing every rewritten ref and replacing or carefully rebasing every existing clone, or (2) creating a fresh repository from a sanitized tree and retiring the current repository. No history rewrite has been run; owner choice is required first.

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

## Prompt 8

- Exact local accounting uses `/apply-template` followed by `/tokenize` with special-token parsing. A fresh request measured `1,076` estimated and `1,076` server prompt tokens (drift `0`); subsequent native tool-call/result groups also held drift at `0`. The initial category split was system `168`, tools `887`, memory `0`. The `remember` schema costs `266` rendered tokens.
- Honest degradation was exercised both ways. With `tokenize` unavailable, three turns converged from drift `110` to `106` to `101`. With `apply_template` unavailable, the required ChatML-shaped overheads and JSON-schema fallback produced drifts `311`, `331`, and `351`; every affected category was marked estimated. Forced `context.accounting: "estimated"` on the capable local profile completed normally and converged to drift `97` on `1,258` measured prompt tokens (`7.71%`). Exact mode plus a missing tokenizer refused the run with the documented not-runnable reason. Defaults are restored to `auto` with both capabilities enabled.
- `SERVING.md` reports `/tokenize` at `8 ms` idle and `8 ms` busy, `/apply-template` at `11 ms` idle and `12 ms` busy, and `tokenize_blocks_on_slot=no`. AgentB reads these Facts at startup; because the tokenizer does not block the generation slot, it emits no avoidance warning and retains exact-by-default accounting.
- Elision on a twelve-file fixture reduced a `44,843`-token context to `15,975` and the run completed. Summary compaction inserted the progress note at message index 1 and reduced `15,139` to `14,921`, then completed. Compaction remains deliberately batched, oldest-first, to limit prefix-cache invalidation.
- With an effective context of `6,144` and reserve `4,096`, a large-file run made one valid request, received one response, and refused the unsent second request with `context_ceiling`: `prompt 4760 tokens + reserve 4096 exceeds n_ctx 6144 after compaction`. It reported one completed turn, no server overflow, and drift `0`. The normal `32,768` context and `10,240` reserve are restored.
- A live `remember` call wrote `Tests are run with \`go test ./...\``. The next session injected it with memory cost `21`; disabling memory removed both prompt text and cost. A twenty-note cap fixture produced the exact omission marker at `60` tokens. The fixture was removed and the original single durable note restored under the normal `1,500`-token cap.
- With reasoning preservation on, request 2 included one prior current-run reasoning message and request 3 included two; the first request of the next run included none. The identical preserve-off task included none on all three requests. Cached prompt tokens were `1071/1184/1259` with preservation and `822/1107/1171` without it; both runs completed. Preservation is restored off.
- Verification passed `go build ./...`, `go vet ./...`, the existing tool tests, and syntax checks for every browser module. Deferred exactly as scoped: settings sheet, `/chat`, replay, memory pin/forget, retrieval, a local tokenizer, additional tool behavior, and OS-level sandboxing.

## Prompt 9

- The gear now opens the fixed-order Servers, Sessions, Tools, Memory, Context, Run, Shell, and Session groups in a 420px right-hand well. Headless Chromium at 1440×900 verified the 160 ms overlay, 120 ms profile expansion, body lock, focus entry, Escape close/focus return, all effort buttons, the memory path, JSONL/chat controls, and no page or resource errors.
- Settings post partial configuration and re-render from the event store. A deliberately invalid effort remained visible with `servers.local.reasoning.effort` placement and the server reason; invalid reserve values likewise use a dotted profile-id path. Tool and shell instances now take validated configuration updates live, so changes apply to the next schema and call without restarting the process.
- Setting effort to `low` persisted in `harness.json`; the next live `model.request.params.reasoning.effort` was `low` and completed `done`. Minimal probe mode completed in `1,026 ms`, updated `probed_at`, and produced six explicitly assumed findings; the sheet showed a Trace probe lamp, then the findings. Full mode was re-probed and restored with 32,768 context, native tool calls, overflow errors, and `low/medium/xhigh` efforts.
- Accounting set to `estimated` showed `estimated — by choice`, seven outlined rail segments, and an estimated session budget; `auto` is restored. A port-9 profile recorded the connection failure, showed Alarm, and session creation was refused with `context length unknown` before workspace creation.
- Duplicating `local`, relabeling it, creating a session, and running a short task succeeded. Removal while used returned `profile in use by session s4`; after closing the session the profile was removed. The last-profile guard remains in place.
- With `read_file.max_limit=50`, the next request advertised schema maximum `50` and the exact verification task completed through the capped tool. The default `400` is restored. The Memory group showed `memory\\sandbox-34710244.md`, the durable `go test ./...` note, and the documented `266`-token `remember` schema.
- The full Go test suite, `go vet`, browser-module syntax checks, and two headless browser passes are clean. Deferred as scoped: `/chat`, replay, memory editing, and changes to loop/tool semantics.

## Prompt 10

- The standalone `/chat` window was verified first with the main page closed. The exact task called `list_dir` and `read_file`, showed two compact `ok` ticks, streamed thinking and final text with the caret, then rendered `stopped: done`; opening `/` afterward rebuilt six State rows, two model rows, and both tool rows from its snapshot. The no-parameter route renders the native session picker.
- Two bound chat windows stayed isolated. Refreshing chat A during its 2,000-line stream retained the existing partial text and caret from the snapshot; `Ctrl+.` stopped only A in `125 ms`, while chat B completed `done` and neither window displayed the other's task or response.
- Two recorded JSONLs replayed as sessions `s2` and `s3` with `replay` in both tabs and headers. State, Timeline, chat, budgets, tools, and stage motion reconstructed from the merged recording; a direct message POST returned 409 `{"error":"replay mode"}` and `?instant=1` delivered both stops in about `30 ms`. No probe, model client, tool registry, or event writer is created in replay mode.
- Motion audit — done: the continuous animations are only queued/call-model breathe plus the streaming caret; the one-shot rail pulse remains part of compaction. The traveling dot produced a non-zero 260 ms transform before the destination lamp filled; sheet/row/rail timings are 160/120/160 ms. Reduced-motion verification hid the dot and reported `0s` animation/transition durations.
- Tell audit — done: CSS contains exactly the six palette hex values, no uppercase transform, only inset recess shadows, and only 1px/2px radii. Colour remains state rather than identity; no cards, hover lifts, skeletons, or new decorative effects were added.
- Typography/assets/empty states — done: CSS font sizes are only 12/13/14/18 and weights only 400/500; instrument numbers use Plex Mono/tabular figures. All three geometric Mute SVG slots exist at 24/20/96 px. Fresh Flow, State, Timeline, and chat show the specified invitation/empty copy.
- Focus/errors/resilience — done: all controls, tabs, close glyphs, rows, chips, links, and 32px-hit-area switches have a 1px Trace focus ring at 2px offset. Transport/reconnect, model error detail, not-runnable reasons, approval controls, and queue notices are visible on both relevant surfaces. Mid-run refresh restoration and stopped-run caret removal were exercised.
- `serve/chat-window.ps1` found Chrome at `C:\Program Files\Google\Chrome\Application\chrome.exe`; it prefers Chrome, then Edge, and falls back to the normal URL. The POSIX launcher checks Chrome/Chromium/Edge, then `open` or `xdg-open`.
- Final deferred list: OS-level shell sandbox; API authentication when the bind leaves loopback; MCP; the Discord process; session persistence; orchestration/handoff; Electron/Tauri; image input; and a second local server slot. The existing message/SSE API and `run.queue_depth` are the two documented Discord-facing hooks.

## HOMEPC tailnet deployment

- HOMEPC now runs the selected `qwen3.8-27b` llama.cpp model from the `AgentB llama-server` startup task. A forced task stop/start returned to healthy service and the next completion succeeded, proving the endpoint is not tied to the installation SSH session.
- llama-server listens on all local interfaces so HOMEPC retains loopback access, while the only matching inbound firewall allow rule restricts TCP 8080 to HOMEPC's Tailscale address `198.51.100.10` and this workstation's Tailscale address `198.51.100.20`.
- Direct calls from this workstation passed model discovery and reported 32,768 context. Non-streaming chat returned exactly `REMOTE_LOCAL_OK_7`; streaming chat returned exactly `STREAM_LOCAL_OK_11` and included completion, usage, timings, prompt progress, and the terminal SSE marker. After task restart, chat returned exactly `RESTART_LOCAL_OK_17`.
- AgentB itself was started on this workstation with `harness.homepc.json`. Its full probe kept the profile runnable with exact accounting, and a one-turn streamed run returned exactly `AGENTB_REMOTE_LOCAL_OK_13`, recorded 1,075 prompt and 44 completion tokens with zero drift, and stopped `done` in 1,924 ms.
