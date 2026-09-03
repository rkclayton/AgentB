# AgentB

AgentB is a small, standard-library Go coding-agent harness for OpenAI-compatible model servers. It provides observable multi-session runs, seven guarded workspace tools, exact-or-labeled context accounting, compaction, durable workspace notes, a console UI, and a standalone chat window.

## Run

On the verified Windows host, start the pinned local model with `powershell -ExecutionPolicy Bypass -File .\serve\start.ps1`. Adjust the script parameters if llama.cpp or the GGUF lives elsewhere. Then start AgentB from the repository root:

```text
go run ./cmd/harness -config harness.json
```

Open `http://127.0.0.1:8790/` for the instrument or `http://127.0.0.1:8790/chat?session=main` for chat. `serve/chat-window.ps1 main` or `serve/chat-window.sh main` launches Chrome/Edge in a 520×760 app window when available and otherwise opens the normal browser.

To run AgentB on this workstation while inference stays on HOMEPC, connect both machines to the same tailnet and use the checked-in client profile:

```text
go run ./cmd/harness -config harness.homepc.json
```

That profile calls `http://198.51.100.10:8080` directly. HOMEPC runs llama-server as the `AgentB llama-server` startup task; its Windows Firewall rule accepts port 8080 only through HOMEPC's Tailscale address and only from this workstation's Tailscale address. If either Tailscale IP changes, rerun `serve/install-windows-task.ps1` on HOMEPC with the new addresses.

Replay one or more session logs without loading a model or enabling mutations:

```text
go run ./cmd/harness -config harness.json -replay logs/main.jsonl,logs/s2.jsonl
```

Profiles hold the endpoint, model, sampling, reasoning, context, and probe results. The settings sheet can add, duplicate, edit, test, and remove profiles; full probes measure behavior while minimal/off modes label assumptions. A profile is runnable only with known context, streaming, structured tool calls, and non-truncating overflow behavior.

Sessions are ephemeral views onto a workspace and may use different profiles. Point several sessions at one workspace for a swarm; file-write conflicts force a re-read instead of silently overwriting another session. AgentB schedules two runs by default, but the verified llama.cpp server uses `--parallel 1`, so local generation interleaves rather than decoding two requests simultaneously.

## Configuration

| Area | Keys |
|---|---|
| Process | `listen`, `workspace`, `log_dir` |
| Profiles | `servers[].{id,label,base_url,model,api_key,request_timeout_s,probe_mode,sampling,reasoning,context,system_prompt_override,capabilities}` |
| Runs | `run.{max_turns,cycle_window,max_consecutive_tool_errors,max_concurrent,queue_depth}`, `approval.mode` |
| Context and memory | `context.{soft_pct,summary_pct,accounting}`, `memory.{enabled,dir,max_tokens}` |
| Tool caps | `tools.{read_file,list_dir,grep}`, `shell.{command,timeout_s,max_timeout_s,max_output_lines_head,max_output_lines_tail,deny}` |

See [web/DESIGN.md](web/DESIGN.md) for the UI contract, [INTERFACES.md](INTERFACES.md) for events and APIs, and [SERVING.md](SERVING.md) for the verified model host. Replace any file in `web/assets/` to change the artwork; nothing else references it.

## Deferred boundaries

- OS-level shell sandboxing; the current jail, deny list, approvals, and process-tree timeout are not a security boundary.
- API authentication. AgentB intentionally binds loopback; an EventSource query-string token was rejected because it leaks through logs/history without addressing a present network threat. Authentication must be designed when `listen` moves off loopback.
- MCP tools and the Discord process. The two Discord integration hooks are the existing `/api/message` plus SSE client surface, and `run.queue_depth`/`message.queued` for chat backpressure; no bot process is included.
- Session persistence across restarts and orchestration or handoff between sessions.
- Electron/Tauri packaging, image input, and a second local `--parallel 2` server slot.
