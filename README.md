# Agent_b

Agent_b is a small Go coding agent for OpenAI-compatible model servers: one binary, a dependency-free browser interface, durable multi-chat work, and observable tool-using runs.

It keeps planning and execution distinct. An operator or planner owns the ordered work in `PLAN.md`; the worker executes the current order, records durable findings in append-only `NOTES.md`, and stops at explicit decision boundaries. Chat is for the work itself, Console exposes the live run, and Settings owns connections and host controls.

![Agent_b Chat with the agent tab strip](docs/images/chat.png)

## Quickstart

1. Install Go 1.24+ and provide an OpenAI-compatible endpoint. Agent_b can use Ollama, LM Studio, vLLM, llama.cpp, or a hosted API; it does not download or manage models.
2. On Windows, double-click `install-Agent_b.cmd` for the normal installation, or `start-Agent_b.cmd` to build and run from a checkout.
3. Open Settings → Connections, add the endpoint and model, Save, then Test. A connection becomes runnable when its context size and required capabilities are known.

The installed app opens Chat at `http://127.0.0.1:8790/chat`. Reopening its shortcut focuses the healthy instance instead of starting another one. Replay recorded sessions without a model with:

```text
go run ./cmd/harness -config harness.json -replay logs/main.jsonl
```

## What it provides

- Durable open and closed chats, workspace memory, compaction, concurrent scheduling, and model-profile failover for summaries.
- Exact context accounting when the server exposes `/tokenize` and `/apply-template`; otherwise every estimate is labeled.
- Twelve tools in stable order: `read_file`, `list_dir`, `write_file`, `edit_file`, `search_text`, `shell`, `remember`, `recall`, `fetch_url`, `find_files`, `run_script`, and `call_service`.
- A Windows service-account boundary for shell and file tools, with explicit operator-identity decisions when work needs the launching user.
- Local attachment ingestion, OCR/extraction sidecars, delivered-file links, replay, and append-only event evidence.

## Choose a serving path

Any compatible endpoint is enough for the agent loop. llama.cpp additionally supports the full accounting and performance instrumentation; copy `serve/local.env.example` to ignored `serve/local.env`, set `MODEL_PATH` and `LLAMA_SERVER`, then run `serve/start.ps1` or `serve/start.sh`.

The reliability probe under `serve/probes/reliability/` scores a candidate model on repeated tool-using repair tasks. Results describe that model and machine, not a general recommendation.

## Documentation

- [Detailed installation, operation, configuration, model setup, and uninstall reference](docs/REFERENCE.md)
- [Windows host-hardening runbook](docs/HARDENING.md)
- [Security boundary and threat model](SECURITY.md)
- [Events, APIs, configuration, and tool contracts](INTERFACES.md)
- [Browser design contract](web/DESIGN.md)
- [Operator-file mailbox and bookshelf](docs/OPERATOR-FILES.md)
- [Third-party notices](docs/THIRD_PARTY_NOTICES.md)

The normal Windows installer separates immutable application files under `%ProgramFiles%`, operator-owned state under `%LocalAppData%`, and the service workspace under `%ProgramData%`. UAC, ownership checks, upgrade preservation, alpha deployment, launcher modes, cleanup, capability degradation, deferred boundaries, and the desktop-packaging decision remain documented in the [detailed reference](docs/REFERENCE.md).

## Development

```text
go test ./...
npm ci
npm run test:ui
```

Node and Playwright are development-only. The shipped browser code has no runtime dependencies, and the application remains one Go process and one binary.

## License

Agent_b is licensed under the [MIT License](LICENSE). Dependency licenses are listed in [third-party notices](docs/THIRD_PARTY_NOTICES.md).
