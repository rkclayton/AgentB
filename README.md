# Agent_b

Agent_b is a small Go coding agent for OpenAI-compatible model servers. It provides observable multi-session runs, ten governed tools, exact-or-labeled context accounting, compaction, durable workspace notes, and a unified Chat, Console, and Settings interface. The browser remains dependency-free; the backend uses the listed TLS-fingerprinting and HTML parsing modules for `fetch_url`.

Choose one serving path before you start.

On Windows, double-click **`install-Agent_b.cmd`** once for the normal installation and approve its same-user UAC prompt. The installer refuses over-the-shoulder elevation by a different administrator. It puts immutable application files under `%ProgramFiles%\Agent_b`, operator-owned configuration and state under `%LocalAppData%\Agent_b`, and the service-account workspace under `%ProgramData%\Agent_b\workspace`. A first install creates `harness.json` from the installed template without importing or overwriting private data; upgrades preserve all existing data. The Start Menu shortcut and Installed apps registration remain per-operator (HKCU) because the config and DPAPI credential belong to that operator. Open **Agent_b** from Start afterward; it opens Chat first, with Console and Settings in the same branded application window. Reopening the shortcut focuses that window and never starts a second healthy server; an unresponsive existing process is reported by PID and left for the operator instead of being killed automatically.

Developers can instead double-click **`start-Agent_b.cmd`** to build and run directly from the checkout. Both paths find Go on `PATH` or in the ignored local `.tools\go` directory. No PowerShell command is required.

The normal launcher console owns the Agent_b process, so it remains open while the server is running and closes with it. An installed launch failure is appended to `%LocalAppData%\Agent_b\logs\launcher-errors.log`; a source launch writes beneath the checkout. The wrapper shows the error for 10 seconds and closes automatically instead of waiting for a keypress. Test automation should use `Agent_b.cmd -Detached -NoBrowser -NoPause` (or the same switches with `start-Agent_b.cmd`): the server starts in a hidden background process and the launcher returns after its readiness check. A detached server does not stop when its browser closes, so automation must stop the exact Agent_b process it started after confirming sessions are idle.

Agent_b refuses to start with an elevated Administrator token. Membership in the local Administrators group is fine: double-click the launcher normally, without **Run as administrator** and outside an elevated terminal.

To remove the installed application, use **Settings → Apps → Installed apps → Agent_b → Uninstall**. The uninstaller asks whether to remove operator data and the service workspace; choosing **No** removes only the application and keeps both data roots for a later reinstall. A purge refuses to delete an operator data root owned by another Windows user. The service account and host firewall policy may be shared, so remove Host protections in Agent_b Settings before uninstalling if they are no longer needed.

## Path A — an endpoint you already run (~5 minutes)

Prerequisites: Go 1.24+ and an OpenAI-compatible endpoint you already run, such as Ollama, LM Studio, vLLM, or a hosted API. Nothing else is required: no GPU, CUDA, or model download. The first source build needs access to the Go module proxy (or a populated module cache) for the `fetch_url` tool's pinned dependencies; running the built binary does not.

```text
git clone <repo-url>
cd AgentB
go build -o Agent_b ./cmd/harness
./Agent_b
```

For a source checkout, the first start copies `harness.example.json` to the ignored local `harness.json`; the checkout remains both the application and data root for development. Under **Connections**, set `base_url` and `model` (and `api_key` when needed), select **Save**, then select **Test**. Startup never waits on a model probe; a successful explicit test stays labeled `ready`, and the first ready profile becomes the initial session route. Settings → Security can create the local service account, verify the identity split, and apply/verify the application, data, workspace, and outbound firewall policies through Windows UAC without running Agent_b itself as Administrator; progress and errors remain visible after refresh. Installed host protection uses the separated roots documented above. Follow the [Windows host hardening](docs/HARDENING.md) sequence. If the endpoint does not report its context size, enter its documented limit in `n_ctx_override`; Agent_b refuses to guess a context ceiling.

The system prompt includes the current date, OS timezone, and (when Windows provides it) a coarse region code. It does not request precise device location or invent a city; location-specific tasks should name their location when the OS does not provide one.

Without llama.cpp's accounting endpoints, the budget meter uses calibrated estimation instead of exact categories, the cached-token readout is hidden, and the prefill and tok/s readouts stay dark. The agent loop, tools, approvals, sessions, memory, compaction, chat, and replay remain available once the endpoint passes the baseline profile checks. See [Capability degradation](#capability-degradation) for the complete behavior.

## Path B — local llama.cpp with full instrumentation (~1 hour)

Prerequisites: Go 1.24+, an NVIDIA GPU with a CUDA 12.8+ driver, about 15 GB of free disk, `curl`, a current llama.cpp `llama-server`, and a GGUF model you supply.

Copy `serve/local.env.example` to the ignored `serve/local.env`, set `MODEL_PATH` and `LLAMA_SERVER`, and adjust `CTX`, `KV_TYPE`, `PORT`, or `MTP` if needed. Start the model with `serve/start.ps1` on Windows or `serve/start.sh` on Unix, then build and run Agent_b as in Path A; set the profile's model to the `MODEL_ALIAS` value before selecting **Test**.

Exact context accounting requires llama.cpp's `/tokenize` and `/apply-template` endpoints. This path exposes both, which is why it can provide the real per-category meter along with cached-token, prefill, and generation-rate instrumentation. Prompt 1 produces the machine-local `SERVING.md`; [SERVING.example.md](SERVING.example.md) shows its public-safe Facts shape.

For either path, Node.js is optional and is used only for `node --check` verification of the dependency-free frontend JavaScript. The four IBM Plex WOFF2 files are committed under `web/assets/fonts/`, so building and serving the UI never contacts npm or another font host.

## Use Agent_b

The installed application opens its Chat view at `http://127.0.0.1:8790/chat`; its compact header switches between Chat, Console, and Settings without another tab or window. Edge app mode keeps its small native title bar and reliable Windows minimize, maximize/restore, and close controls; a true installed-PWA host may merge the application header into that area through Window Controls Overlay. The Console route remains available directly at `http://127.0.0.1:8790/`, and `?session=main` binds Chat to a specific session.

Replay one or more session logs without loading a model or enabling mutations:

```text
go run ./cmd/harness -config harness.json -replay logs/main.jsonl,logs/s2.jsonl
```

Profiles hold an endpoint, model, sampling, reasoning, context settings, and measured capabilities. The settings sheet can add, duplicate, edit, test, and remove profiles; full probes measure behavior while minimal/off modes label assumptions. A profile is runnable only with known context, streaming, structured tool calls, and non-truncating overflow behavior.

Sessions are ephemeral views onto a workspace and may use different profiles. Point several sessions at one workspace for a swarm; file-write conflicts force a re-read instead of silently overwriting another session. Agent_b schedules two runs by default, but a llama.cpp server started with `--parallel 1` interleaves their slot work instead of decoding two requests simultaneously.

With the service-account split enabled, shell and built-in file tools use the `agentb-svc` Windows identity. File tools accept absolute paths when that account's Windows permissions allow them; with the split disabled, `internal/tools/jail.go` remains their sole workspace constraint and must not be removed. Shell is never workspace-confined: the workspace is only its initial working directory, so `cd ..` and absolute paths remain possible. One-off shell work must be passed inline: shell refuses execution-policy bypass, refuses commands that create executable script artifacts, and will not execute a script file written through an agent file tool during the current run. Pre-existing operator/repository scripts remain runnable. ACLs restrict what a service-account shell can reach, not what it can do with reachable resources, so every shell call requires a policy confirmation while the split is enabled regardless of `approval.mode`. A subsequent permission or identity failure pauses again and offers the distinct **Run once as operator** escape. The failed command or file call is never retried automatically. Escape approval reruns only the displayed operation once under the Windows account running Agent_b, is logged, and reports operator context. It does not grant lasting permission or run as Administrator.

The operator control beside Stop is the sole persistent indicator and toggle for the longer-lived operator mode. Its off/on image follows server events in both Console and Chat. Enabling requires confirmation and runs subsequent shell and file tools as the non-elevated Windows account that launched Agent_b; disabling is immediate. There is no absolute ceiling: the grant lapses after 20 minutes without agent tool activity by default, every ordinary tool execution resets that idle deadline at start and completion, and process exit always revokes it. A single call running beyond the idle window can therefore lapse while still executing. The one-shot **Run once as operator** escape neither creates a grant nor resets this clock.

Use `fetch_url` for public HTTP/HTTPS text. It sends GET requests without model-supplied headers, cookies, or credentials; extracts readable HTML; refuses binary responses and private, loopback, or link-local destinations; and marks every result as untrusted external data. Results are UTF-8-safe byte windows: pass the returned `next_offset` unchanged as `offset` to receive the next non-overlapping window. `read_file` uses the same cursor convention while adding source line numbers and explicit mid-line boundary markers to each local-file window. `tools.fetch.allow_domains` optionally limits public domains (an empty list permits all public domains), while `allow_internal_hosts` is an exact-host exception for deliberately configured private endpoints. Defaults are a 20-second request timeout, five redirects, a 2 MiB response cap, a 16 KiB return window, and a 64 KiB maximum window.

## Bring your own model

`serve/probes/reliability/` is an onboarding check for the question “can my model handle tool calling well enough?” It generates a small Go repair fixture, runs two tool-using tasks three times each, and reports a score as passes out of six using explicit completion, tool-choice, argument, and turn-count rules.

Provide two candidate GGUF paths and run the platform script:

```text
# PowerShell
$env:C1_MODEL = '<first-candidate.gguf>'
$env:C2_MODEL = '<second-candidate.gguf>'
.\serve\probes\reliability\run.ps1

# Unix shell
C1_MODEL='<first-candidate.gguf>' C2_MODEL='<second-candidate.gguf>' serve/probes/reliability/run.sh
```

The generated JSONL, workspaces, server logs, score sheets, and summary stay under the ignored `serve/probes/reliability/runs/` directory. Use the numeric result as evidence for your own model and hardware rather than as a general model recommendation.

## Capability degradation

| Missing capability | Behavior |
|---|---|
| `/tokenize` | Budget categories use a calibrated estimate, remain visibly estimated, and retain a guard margin. |
| `/apply-template` | Per-role and schema overhead use documented estimates; no value is presented as exact. |
| tool-aware `/apply-template` | The tools category alone is estimated. |
| cached-token reporting | The cached-token readout is hidden rather than displayed as zero. |
| timings or prompt progress | Prefill and tok/s readouts stay dark; elapsed time remains available. |
| structured tool calls, streaming, or known context | The profile is `not_runnable` and states what must be fixed. |
| silent context truncation | The profile is refused; Agent_b never silently truncates a prompt. |

## Configuration

Startup locates configuration in this order: an explicit launcher `-config` argument, `AGENTB_CONFIG`, `%LocalAppData%\Agent_b\harness.json` when it exists, then `harness.json` relative to the current directory as the deliberate development fallback. The first-run template is resolved from the application root, independently of the live config location. Installed relative log and memory paths resolve beneath the LocalAppData data root; the workspace is the explicit ProgramData path.

The former `%LocalAppData%\Programs\Agent_b` layout is not migrated automatically. Close and uninstall the old copy while preserving its data if needed, install the new layout, re-enter connection settings and the service password, apply Host protections, and verify the new installation before deleting any preserved old data.

| Area | Keys |
|---|---|
| Process | `listen`, `workspace`, `log_dir` |
| Profiles | `servers[].{id,label,base_url,model,api_key,request_timeout_s,probe_mode,sampling,reasoning,context,system_prompt_override,capabilities}` |
| Runs | `run.{max_turns,cycle_window,max_consecutive_tool_errors,max_concurrent,queue_depth}`, `approval.mode` |
| Context and memory | `context.{soft_pct,summary_pct,accounting}`, `memory.{enabled,dir,max_tokens}` |
| Tool caps | `tools.{read_file:{default_limit,max_limit},list_dir,grep,fetch:{timeout_s,max_bytes,max_redirects,default_limit,max_limit,allow_domains,allow_internal_hosts}}`, `shell.{command,timeout_s,max_timeout_s,max_output_lines_head,max_output_lines_tail,file_routing_guard,operator_context,operator_context_idle_timeout_minutes,service_account,deny}` (`read_file` and `fetch_url` limits are bytes; `fetch_url` defaults to public hosts with private-network access denied; `file_routing_guard` defaults on; `operator_context` and `service_account.enabled` default off; operator context is process-global, runtime-only, and lapses after 20 idle minutes by default) |

See [web/DESIGN.md](web/DESIGN.md) for the UI contract, [INTERFACES.md](INTERFACES.md) for events and APIs, and [SECURITY.md](SECURITY.md) plus [Windows host hardening](docs/HARDENING.md) before granting a model shell access. The UI uses only the six-color industrial-console system; artwork and vendored fonts live under `web/assets/`.

`approval.mode` accepts `boundary-only` (the default; no generic confirmations), `mutating` (confirm `write_file`, `edit_file`, and `shell`), and `all` (confirm every tool call). The deprecated `off` value remains accepted as an alias for `boundary-only` but is hidden from Settings. Service-account mode adds the independent shell policy confirmation described above in all four modes. With the service account disabled, shell follows the selected mode normally: silent under `boundary-only`/`off`, prompted under `mutating`/`all`. No mode disables the mandatory **Run once as operator** decision after a Windows identity or permission denial.

Dependency licenses and included transitive modules are recorded in [docs/THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).

## Deferred boundaries

- OS-level shell sandboxing; the shell is not jailed, and its file routing, deny list, approvals, and process-tree timeout are not a security boundary.
- API authentication. Agent_b intentionally binds loopback; an EventSource query-string token was rejected because it leaks through logs/history without addressing a present network threat. Authentication must be designed when `listen` moves off loopback.
- MCP tools and the Discord process. The two Discord integration hooks are the existing `/api/message` plus SSE client surface, and `run.queue_depth`/`message.queued` for chat backpressure; no bot process is included.
- Session persistence across restarts and orchestration or handoff between sessions.
- Image input and a second local `--parallel 2` server slot.

## Decided against

**Desktop packaging (Electron/Tauri), 2026-09-04.** Evaluated and rejected. This is a decision, not a deferral. Packaging would have supplied global hotkeys, tray-resident operation with OS notifications, native file dialogs, taskbar progress and badges, single-instance enforcement, a pinned Chromium, and guaranteed freedom from background throttling.

Background throttling was the only concrete defect it would have fixed, and it was fixed at the correct layer instead: clients reconcile operator state from `/api/state` on every SSE open and foreground resume, and operator events render synchronously rather than waiting on an animation frame. Attachment ingest, the one capability treated as mandatory, does not require packaging either. Drag-and-drop, clipboard paste, and the file picker all deliver file contents to a browser client, and an attachment must be copied into the workspace in either case, because one left outside it defeats `internal/tools/jail.go`. The only thing a browser cannot supply is the attachment's source path, and that limitation is the workspace boundary working as intended.

Against that, packaging adds a bundled Chromium and a Node toolchain to a project whose rules are one process, one binary, and a dependency-free browser; it makes Chromium patching a local responsibility; it assumes a local operator, which conflicts with any later move off loopback; and its main process runs as the operator with full Node access beside the OS-level boundary this project treats as its security model. The remaining benefits, tray residency, notifications, and an always-on-top chat window, are polish and do not carry the trade on their own. Revisit only if a requirement appears that a browser client genuinely cannot serve.
