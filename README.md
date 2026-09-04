# Agent_b

Agent_b is a small, standard-library Go coding agent for OpenAI-compatible model servers. It provides observable multi-session runs, eight guarded workspace tools, exact-or-labeled context accounting, compaction, durable workspace notes, an industrial-console UI, and a standalone chat window.

Choose one serving path before you start.

On Windows, double-click **`install-Agent_b.cmd`** once for the normal per-user installation. It builds Agent_b, creates a branded Start Menu shortcut, and registers **Agent_b** in Windows Installed apps without requiring administrator elevation. A first install imports this checkout's local connection settings and user-scoped service credential when present; upgrades preserve the installed settings. Open **Agent_b** from Start afterward.

Developers can instead double-click **`start-Agent_b.cmd`** to build and run directly from the checkout. Both paths find Go on `PATH` or in the ignored local `.tools\go` directory. No PowerShell command is required.

Agent_b refuses to start with an elevated Administrator token. Membership in the local Administrators group is fine: double-click the launcher normally, without **Run as administrator** and outside an elevated terminal.

To remove the installed application, use **Settings → Apps → Installed apps → Agent_b → Uninstall**. The uninstaller asks whether to remove local connection settings, credential, logs, memory, and workspace; choosing **No** keeps them for a later reinstall. The service account and host firewall policy may be shared, so remove Host protections in Agent_b Settings before uninstalling if they are no longer needed.

## Path A — an endpoint you already run (~5 minutes)

Prerequisites: Go 1.24+ and an OpenAI-compatible endpoint you already run, such as Ollama, LM Studio, vLLM, or a hosted API. Nothing else is required: no GPU, CUDA, model download, or network access at build time.

```text
git clone <repo-url>
cd AgentB
go build -o Agent_b ./cmd/harness
./Agent_b
```

The first start copies the legacy-compatible `harness.example.json` filename to the ignored local `harness.json`; existing connection settings continue to work unchanged. Under **Connections**, set `base_url` and `model` (and `api_key` when needed), select **Save**, then select **Test**. A successful test stays labeled `ready`; choose that profile for the existing session under **Sessions**. Settings → Security can create the local service account, verify the identity split, and apply/verify the application ACL and outbound firewall policies through Windows UAC without running Agent_b itself as Administrator; progress and errors remain visible after refresh. Follow the [Windows host hardening](docs/HARDENING.md) sequence. If the endpoint does not report its context size, enter its documented limit in `n_ctx_override`; Agent_b refuses to guess a context ceiling.

Without llama.cpp's accounting endpoints, the budget meter uses calibrated estimation instead of exact categories, the cached-token readout is hidden, and the prefill and tok/s readouts stay dark. The agent loop, tools, approvals, sessions, memory, compaction, chat, and replay remain available once the endpoint passes the baseline profile checks. See [Capability degradation](#capability-degradation) for the complete behavior.

## Path B — local llama.cpp with full instrumentation (~1 hour)

Prerequisites: Go 1.24+, an NVIDIA GPU with a CUDA 12.8+ driver, about 15 GB of free disk, `curl`, a current llama.cpp `llama-server`, and a GGUF model you supply.

Copy `serve/local.env.example` to the ignored `serve/local.env`, set `MODEL_PATH` and `LLAMA_SERVER`, and adjust `CTX`, `KV_TYPE`, `PORT`, or `MTP` if needed. Start the model with `serve/start.ps1` on Windows or `serve/start.sh` on Unix, then build and run Agent_b as in Path A; set the profile's model to the `MODEL_ALIAS` value before selecting **Test**.

Exact context accounting requires llama.cpp's `/tokenize` and `/apply-template` endpoints. This path exposes both, which is why it can provide the real per-category meter along with cached-token, prefill, and generation-rate instrumentation. Prompt 1 produces the machine-local `SERVING.md`; [SERVING.example.md](SERVING.example.md) shows its public-safe Facts shape.

For either path, Node.js is optional and is used only for `node --check` verification of the dependency-free frontend JavaScript. The four IBM Plex WOFF2 files are committed under `web/assets/fonts/`, so building and serving the UI never contacts npm or another font host.

## Use Agent_b

The main instrument is at `http://127.0.0.1:8790/`, and a bound chat window is at `http://127.0.0.1:8790/chat?session=main`. `serve/chat-window.ps1 main` or `serve/chat-window.sh main` opens a 520×760 Chrome/Edge app window when available and otherwise opens the normal browser.

Replay one or more session logs without loading a model or enabling mutations:

```text
go run ./cmd/harness -config harness.json -replay logs/main.jsonl,logs/s2.jsonl
```

Profiles hold an endpoint, model, sampling, reasoning, context settings, and measured capabilities. The settings sheet can add, duplicate, edit, test, and remove profiles; full probes measure behavior while minimal/off modes label assumptions. A profile is runnable only with known context, streaming, structured tool calls, and non-truncating overflow behavior.

Sessions are ephemeral views onto a workspace and may use different profiles. Point several sessions at one workspace for a swarm; file-write conflicts force a re-read instead of silently overwriting another session. Agent_b schedules two runs by default, but a llama.cpp server started with `--parallel 1` interleaves their slot work instead of decoding two requests simultaneously.

With the service-account split enabled, a shell permission denial or service-identity launch failure pauses and offers **Run once as operator**. The failed command is never retried automatically. Approval reruns only the displayed command once under the Windows account running Agent_b, is required even when normal approvals are off, and is logged. It does not grant a lasting permission or run as Administrator.

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

| Area | Keys |
|---|---|
| Process | `listen`, `workspace`, `log_dir` |
| Profiles | `servers[].{id,label,base_url,model,api_key,request_timeout_s,probe_mode,sampling,reasoning,context,system_prompt_override,capabilities}` |
| Runs | `run.{max_turns,cycle_window,max_consecutive_tool_errors,max_concurrent,queue_depth}`, `approval.mode` |
| Context and memory | `context.{soft_pct,summary_pct,accounting}`, `memory.{enabled,dir,max_tokens}` |
| Tool caps | `tools.{read_file,list_dir,grep}`, `shell.{command,timeout_s,max_timeout_s,max_output_lines_head,max_output_lines_tail,file_routing_guard,service_account,deny}` (`file_routing_guard` defaults on; `service_account.enabled` defaults off) |

See [web/DESIGN.md](web/DESIGN.md) for the UI contract, [INTERFACES.md](INTERFACES.md) for events and APIs, and [SECURITY.md](SECURITY.md) plus [Windows host hardening](docs/HARDENING.md) before granting a model shell access. The UI uses only the six-color industrial-console system; artwork and vendored fonts live under `web/assets/`.

## Deferred boundaries

- OS-level shell sandboxing; the shell is not jailed, and its file routing, deny list, approvals, and process-tree timeout are not a security boundary.
- API authentication. Agent_b intentionally binds loopback; an EventSource query-string token was rejected because it leaks through logs/history without addressing a present network threat. Authentication must be designed when `listen` moves off loopback.
- MCP tools and the Discord process. The two Discord integration hooks are the existing `/api/message` plus SSE client surface, and `run.queue_depth`/`message.queued` for chat backpressure; no bot process is included.
- Session persistence across restarts and orchestration or handoff between sessions.
- Electron/Tauri packaging, image input, and a second local `--parallel 2` server slot.
