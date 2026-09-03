# AgentB

AgentB is a small, standard-library Go coding-agent harness for OpenAI-compatible model servers. It provides observable multi-session runs, eight guarded workspace tools, exact-or-labeled context accounting, compaction, durable workspace notes, an industrial-console UI, and a standalone chat window.

Choose one serving path before you start.

## Path A — an endpoint you already run (~5 minutes)

Prerequisites: Go 1.24+ and an OpenAI-compatible endpoint you already run, such as Ollama, LM Studio, vLLM, or a hosted API. Nothing else is required: no GPU, CUDA, model download, or network access at build time.

```text
git clone <repo-url>
cd AgentB
go build ./cmd/harness
./harness
```

On Windows, run `harness.exe` as `.\harness.exe`. The first start copies `harness.example.json` to the ignored local `harness.json`; open `http://127.0.0.1:8790`, expand **Servers**, set `base_url` and `model` (and `api_key` when needed), then select **Test**. A successful test stays labeled `ready`; choose that profile for the existing session under **Sessions**. If the endpoint does not report its context size, enter its documented limit in `n_ctx_override`; AgentB refuses to guess a context ceiling.

Without llama.cpp's accounting endpoints, the budget meter uses calibrated estimation instead of exact categories, the cached-token readout is hidden, and the prefill and tok/s readouts stay dark. The agent loop, tools, approvals, sessions, memory, compaction, chat, and replay remain available once the endpoint passes the baseline profile checks. See [Capability degradation](#capability-degradation) for the complete behavior.

## Path B — local llama.cpp with full instrumentation (~1 hour)

Prerequisites: Go 1.24+, an NVIDIA GPU with a CUDA 12.8+ driver, about 15 GB of free disk, `curl`, a current llama.cpp `llama-server`, and a GGUF model you supply.

Copy `serve/local.env.example` to the ignored `serve/local.env`, set `MODEL_PATH` and `LLAMA_SERVER`, and adjust `CTX`, `KV_TYPE`, `PORT`, or `MTP` if needed. Start the model with `serve/start.ps1` on Windows or `serve/start.sh` on Unix, then build and run AgentB as in Path A; set the profile's model to the `MODEL_ALIAS` value before selecting **Test**.

Exact context accounting requires llama.cpp's `/tokenize` and `/apply-template` endpoints. This path exposes both, which is why it can provide the real per-category meter along with cached-token, prefill, and generation-rate instrumentation. Prompt 1 produces the machine-local `SERVING.md`; [SERVING.example.md](SERVING.example.md) shows its public-safe Facts shape.

For either path, Node.js is optional and is used only for `node --check` verification of the dependency-free frontend JavaScript. The four IBM Plex WOFF2 files are committed under `web/assets/fonts/`, so building and serving the UI never contacts npm or another font host.

## Use the harness

The main instrument is at `http://127.0.0.1:8790/`, and a bound chat window is at `http://127.0.0.1:8790/chat?session=main`. `serve/chat-window.ps1 main` or `serve/chat-window.sh main` opens a 520×760 Chrome/Edge app window when available and otherwise opens the normal browser.

Replay one or more session logs without loading a model or enabling mutations:

```text
go run ./cmd/harness -config harness.json -replay logs/main.jsonl,logs/s2.jsonl
```

Profiles hold an endpoint, model, sampling, reasoning, context settings, and measured capabilities. The settings sheet can add, duplicate, edit, test, and remove profiles; full probes measure behavior while minimal/off modes label assumptions. A profile is runnable only with known context, streaming, structured tool calls, and non-truncating overflow behavior.

Sessions are ephemeral views onto a workspace and may use different profiles. Point several sessions at one workspace for a swarm; file-write conflicts force a re-read instead of silently overwriting another session. AgentB schedules two runs by default, but a llama.cpp server started with `--parallel 1` interleaves their slot work instead of decoding two requests simultaneously.

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
| silent context truncation | The profile is refused; AgentB never silently truncates a prompt. |

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
- API authentication. AgentB intentionally binds loopback; an EventSource query-string token was rejected because it leaks through logs/history without addressing a present network threat. Authentication must be designed when `listen` moves off loopback.
- MCP tools and the Discord process. The two Discord integration hooks are the existing `/api/message` plus SSE client surface, and `run.queue_depth`/`message.queued` for chat backpressure; no bot process is included.
- Session persistence across restarts and orchestration or handoff between sessions.
- Electron/Tauri packaging, image input, and a second local `--parallel 2` server slot.
