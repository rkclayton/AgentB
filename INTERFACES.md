# Agent_b interfaces

This document is the stable contract for prompts 3–10. Later prompts implement the tagged records without changing their shapes.

## Event envelope and transport

Every event is `{"seq":int,"ts":"RFC3339 with milliseconds","session_id":"","run_id":"","type":"","data":{}}`. Sessionless events are global. SSE uses `event: <type>`, the JSON envelope in `data:`, `id: <seq>`, and a comment ping every 15 seconds. JSONL additionally permits `body` on `model.request` and `raw` on `model.response`; those fields are excluded from SSE. Each session has its own log and global events use the Agent_b log.

On SSE connection, `snapshot` [prompt 3] contains `{sessions:{<id>:SessionSnapshot},servers:[Profile with masked key],config,replay,mutation_token,shell_credential:{stored,stored_at},shell_identity:{fallback,operator_approval_required,operator_context,operator_context_expires_at,reason,since},serving_facts,flow:{stages,edges},tools:[{name,description}]}`. `mutation_token` is a per-launch browser/CSRF secret required in `X-AgentB-Mutation-Token` on every non-read request; it is not persisted or logged. `replay` is true only for `-replay` servers. The credential status never contains the credential.

## Shared records

`SessionSnapshot` is `{id,label,server_id,workspace,run:{status:idle|queued|running|paused|stopping|replay,run_id,turn,max_turns,queue_position,partial},tools:[{name,enabled,calls,schema_tokens}],messages:[Message],budget:Budget,timeline:[last 500 live rows or recorded replay rows],queued_messages,runnable,not_runnable_reason,memory_path,memory_content,log_path}`. `memory_content` is the read-only injected view for the settings sheet; `log_path` names the current session JSONL. `schema_tokens` and memory fields remain zero/empty until prompt 8.

`Message` is `{id,role:system|user|assistant|tool,content,reasoning?,tool_calls?,tool_call_id?,name?,category:system|memory|tools|history|files|results|summary,tokens,estimated,elided,turn}`.

`Budget` is `{n_ctx,reserve,ceiling,used_est,used_measured,drift,cached_last:null|int,mode:exact|estimated,estimated,estimated_categories:[...],categories:{system,memory,tools,history,files,results,summary}}`.

`Profile` has `{id,label,base_url,model,api_key,request_timeout_s,probe_mode,sampling:{thinking,nonthinking},reasoning:{control,enabled,effort,valid_efforts,preserve},context:{n_ctx_override,reserve_output},system_prompt_override,capabilities}`. API keys are always `""` or `"•••• set"` in API/event output and never enter logs.

`Capabilities` has `{server:llama.cpp|openai-compatible|unknown,props,n_ctx,tokenize,apply_template,apply_template_tools,streaming,tool_calls,grammar_constrained,cached_tokens,timings,prompt_progress,reasoning_control:chat_template_kwargs|top_level|server_flag|none,valid_efforts,overflow_behavior:error|truncate|unknown,probed_at,findings}`.

## Events

- `session.created` [prompt 3] `{session}`; `session.renamed` [3] `{session_id,label}`; `session.updated` `{session_id,server_id,runnable,not_runnable_reason,memory_path,memory_content}`; `session.reset` [3] `{session_id,log_path}`; `session.closed` [3] `{session_id}`.
- `server.probed` [3] `{server_id,capabilities,findings}`; `config.changed` [3] `{config}` (masked); `error` [3] `{where,message}`.
- `shell.identity` [17] `{fallback,operator_approval_required,operator_context,operator_context_expires_at,reason,since}` announces or clears a persistent alternate-identity failure and reports session-scoped operator context. New events set `fallback:false`; failed service spawning requires an operator approval instead of executing automatically. `shell.credential` [17] `{stored,stored_at}` reports write-only store status. `operator.context` [19] `{enabled,reason,expires_at}` audits every transition globally and in each live session.
- `run.queued` [4] `{run_id,position}`; `run.started` [4] `{run_id,user_message_id}`; `run.stopped` [4] `{run_id,reason,detail,turns}`. Stop reasons are `done`, `user_stop`, `turn_ceiling`, `cycle`, `tool_errors`, `context_ceiling`, `length`, `model_error`, `profile_not_runnable`.
- `stage` [4] `{stage,state:enter|exit,turn,ms}`. Stages are `assemble`, `call_model`, `parse`, `dispatch`, `execute`, `append`, `compact`, `wait_user`; compact is initially a no-op.
- `model.request` [4] `{turn,message_count,tool_count,params:{temperature,top_p,top_k?,min_p?,presence_penalty,repeat_penalty?,max_tokens,reasoning:{control,effort?,enabled?,preserve?}},est_prompt_tokens,estimated}`.
- `model.progress` [4] `{turn,total,cache,processed}`; `model.delta` [4] `{turn,kind:reasoning|content|tool_call,index,text}`.
- `model.response` [4] `{turn,finish_reason,content,reasoning_tokens,tool_calls:[{id,name,arguments}],usage:{prompt_tokens,completion_tokens,cached_tokens:null|int},timings:null|object,duration_ms}`.
- `tool.call` [4] `{turn,call_id,name,args}`; `tool.result` [4] `{turn,call_id,name,ok,operator_context,ms,bytes,tokens,preview}`; `tool.toggled` [4] `{name,enabled}`. `operator_context:true` marks a shell or file-tool call that actually executed with the Agent_b operator identity.
- `message.appended` [4] `{message}`; `message.updated` [8] `{id,patch}`; `message.queued` [6] `{message_id,position}`.
- `budget` [4] uses the `Budget` shape. Prompt 4 emits a rough estimate; prompt 8 resolves exact versus estimated accounting.
- `approval.required` [6] `{call_id,name,args}`; `approval.decided` [6] `{call_id,decision}`; `cycle.detected` [6] `{call_id,name,args,prior_call_id}`; `workspace.conflict` [6] `{path,session_id,other_session_id,other_label,age_s}`. Dispatcher-only `<tool>.operator_override` approvals use `{command?|path?,identity,reason,scope}` and are mandatory regardless of `approval.mode`; approval reruns only the original tool arguments once under the Agent_b process identity.
- `compaction` [8] `{kind:elide|summarize,before,after,affected_ids,summary_message_id?}`; `memory.noted` [8] `{note,path}`.

## HTTP API

- `GET /` serves Console and Settings, `/chat` serves the default Chat view, and same-window buttons connect those views; `GET /static/*` serves `web/`.
- `GET /api/events` is SSE; `GET /api/state` is `snapshot.data`. In replay mode each SSE connection receives the initial synthetic snapshot followed by the merged, timestamp-ordered recording at 20 ms per event; `?instant=1` removes the delay.
- `GET /api/sessions`; `POST /api/sessions {label?,server_id,workspace?}` → 201 `{session}`; `POST /api/sessions/{id} {label?,server_id?}` → 200 `{session}` (server reassignment requires an idle session and a runnable profile); `DELETE /api/sessions/{id}?force=1`; `POST /api/sessions/{id}/reset`.
- `GET /api/servers`; `POST /api/servers/{id}/probe` → 202 and an eventual event.
- `GET /api/config`; `POST /api/config` deep-merges keys and server entries by immutable `id`, validates, saves, and publishes `config.changed`. The masked API-key sentinel means unchanged. Validation errors are 400 `{error,field}`. `shell.operator_context` is runtime-only, must be posted alone, and is never persisted on; changes to it or `shell.service_account` require a loopback request whose actual client process has the Agent_b operator's Windows SID and is not Agent_b or its descendant. `shell.operator_context_timeout_minutes` is changed only in the protected configuration file while Agent_b is stopped.
- `POST /api/shell-credential {action:store,password}` writes a user-scoped DPAPI blob; `{action:test}` attempts a no-op service-account process; `{action:clear}` removes it. Responses and events expose status only, never the password.
- `GET /api/service-account` inspects the configured local account without elevation. `POST /api/service-account {action:create|reset,password,confirmation}` requires matching write-only values, stores the DPAPI credential, launches `setup-service-account.ps1` through Windows UAC, validates that credential through Windows, and enables the split. It never returns either password field. A canceled or unavailable UAC prompt makes no account change and restores the previous credential; a failure after the elevated helper starts is reported as potentially partial.
- `GET /api/hardening?server_id=<id>` inspects complete-tree ACL and user-scoped outbound-firewall state and includes the most recent `operation {action,state,message,started_at,finished_at}` so progress and errors survive a browser refresh. `POST /api/hardening {action:apply|verify|remove,server_id}` orchestrates the policies; apply requires an enabled, stored non-administrator service identity and no active runs. Apply/remove use UAC; apply grants workspace access, verifies both controls, then tests a real alternate-identity process in that workspace. Apply and verify return `ok:false` with the component summaries when drift remains.
- `POST /api/message`, `/api/stop`, `/api/tools/{name}`, and `/api/approve` drive runs, cancellation, per-session tool toggles, and approval decisions.

## Replay and keyboard

`go run ./cmd/harness -config harness.json -replay <a.jsonl>[,<b.jsonl>…]` validates every JSONL line, reconstructs one `replay` session per file, renumbers merged events, and starts without probing, model clients, tools, or writers. All API GETs remain available; session/config/profile mutations, messages, stops, approvals, and tool toggles return 409 `{"error":"replay mode"}`.

The main page uses `Ctrl+1` through `Ctrl+9` to switch sessions and Escape to close settings. Chat uses `/` to focus the composer, `Ctrl+.` to stop, Enter to send, Shift+Enter for a newline, and Escape to clear the draft. Every interactive control is in normal Tab order with a visible Trace focus ring.

## Behavior notes

- A write from one session blocks another session's `write_file` or `edit_file` until that session re-reads the shared path; the refusal identifies the other session and publishes `workspace.conflict`. When `shell.service_account.enabled` is true on Windows, `read_file`, `list_dir`, `write_file`, `edit_file`, `grep`, and `glob` impersonate that account for their complete filesystem operation; relative paths start at the workspace and absolute paths rely on Windows ACLs. With the split disabled, the workspace path boundary remains in force.
- With `run.queue_depth > 0`, messages posted during a run wait per session and start in order after it ends; `user_stop` discards the remaining messages. Depth `0` keeps the immediate 409 behavior.
- `run.cycle_window: 0` disables repeated-call cycle detection. `run.max_consecutive_tool_errors: 0` disables the consecutive tool-error stop.

## Configuration

Every parsed key appears in `harness.example.json`: `listen`, `workspace`, `log_dir`, `servers`; `run.{max_turns,cycle_window,max_consecutive_tool_errors,max_concurrent,queue_depth}`; `approval.mode`; `context.{soft_pct,summary_pct,accounting}`; `memory.{enabled,dir,max_tokens}`; `tools.{read_file,list_dir,grep}`; and `shell.{command,timeout_s,max_timeout_s,max_output_lines_head,max_output_lines_tail,file_routing_guard,operator_context,operator_context_timeout_minutes,service_account:{enabled,account,domain},deny}`. Files are saved as two-space JSON in this order, except `operator_context` always saves as `false` and starts off. A v1 `server` document migrates to `servers[local]`.

Profiles with `tool_calls=false`, `overflow_behavior=truncate`, `streaming=false`, or unknown context are not runnable. `context.accounting=exact` later refuses profiles without `/tokenize` using `exact accounting requested but this server has no /tokenize`; `auto` chooses exact where possible and calibrated estimation elsewhere; `estimated` forces estimation.

## Deferred: API authentication

The HTTP service binds loopback and has no API authentication. Putting a token into the only practical native `EventSource` transport (`?token=`) would leak it through logs and browser history without protecting against a present network threat. Authentication must be designed when a later Discord/non-loopback transport introduces a real boundary, not added piecemeal.

## Implementation status

Every `[prompt N]` tag and every record/API shape above now has an implementation. The Go replay reducer and browser event reducer carry matching comments so future event-state changes are made in both places.
