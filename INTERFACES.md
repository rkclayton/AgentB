# AgentB interfaces

This document is the stable contract for prompts 3–10. Later prompts implement the tagged records without changing their shapes.

## Event envelope and transport

Every event is `{"seq":int,"ts":"RFC3339 with milliseconds","session_id":"","run_id":"","type":"","data":{}}`. Sessionless events are global. SSE uses `event: <type>`, the JSON envelope in `data:`, `id: <seq>`, and a comment ping every 15 seconds. JSONL additionally permits `body` on `model.request` and `raw` on `model.response`; those fields are excluded from SSE. Each session has its own log and global events use the harness log.

On SSE connection, `snapshot` [prompt 3] contains `{sessions:{<id>:SessionSnapshot},servers:[Profile with masked key],config,flow:{stages,edges},tools:[{name,description}]}`.

## Shared records

`SessionSnapshot` is `{id,label,server_id,workspace,run:{status:idle|queued|running|paused|stopping|replay,run_id,turn,max_turns,queue_position,partial},tools:[{name,enabled,calls,schema_tokens}],messages:[Message],budget:Budget,timeline:[last 200 rows],queued_messages,runnable,not_runnable_reason,memory_path}`. `schema_tokens` and `memory_path` remain zero/empty until prompt 8.

`Message` is `{id,role:system|user|assistant|tool,content,reasoning?,tool_calls?,tool_call_id?,name?,category:system|memory|tools|history|files|results|summary,tokens,estimated,elided,turn}`.

`Budget` is `{n_ctx,reserve,ceiling,used_est,used_measured,drift,cached_last:null|int,mode:exact|estimated,estimated,estimated_categories:[...],categories:{system,memory,tools,history,files,results,summary}}`.

`Profile` has `{id,label,base_url,model,api_key,request_timeout_s,probe_mode,sampling:{thinking,nonthinking},reasoning:{control,enabled,effort,valid_efforts,preserve},context:{n_ctx_override,reserve_output},system_prompt_override,capabilities}`. API keys are always `""` or `"•••• set"` in API/event output and never enter logs.

`Capabilities` has `{server:llama.cpp|openai-compatible|unknown,props,n_ctx,tokenize,apply_template,apply_template_tools,streaming,tool_calls,grammar_constrained,cached_tokens,timings,prompt_progress,reasoning_control:chat_template_kwargs|top_level|server_flag|none,valid_efforts,overflow_behavior:error|truncate|unknown,probed_at,findings}`.

## Events

- `session.created` [prompt 3] `{session}`; `session.renamed` [3] `{session_id,label}`; `session.reset` [3] `{session_id,log_path}`; `session.closed` [3] `{session_id}`.
- `server.probed` [3] `{server_id,capabilities,findings}`; `config.changed` [3] `{config}` (masked); `error` [3] `{where,message}`.
- `run.queued` [4] `{run_id,position}`; `run.started` [4] `{run_id,user_message_id}`; `run.stopped` [4] `{run_id,reason,detail,turns}`. Stop reasons are `done`, `user_stop`, `turn_ceiling`, `cycle`, `tool_errors`, `context_ceiling`, `length`, `model_error`, `profile_not_runnable`.
- `stage` [4] `{stage,state:enter|exit,turn,ms}`. Stages are `assemble`, `call_model`, `parse`, `dispatch`, `execute`, `append`, `compact`, `wait_user`; compact is initially a no-op.
- `model.request` [4] `{turn,message_count,tool_count,params:{temperature,top_p,top_k?,min_p?,presence_penalty,repeat_penalty?,max_tokens,reasoning:{control,effort?,enabled?,preserve?}},est_prompt_tokens,estimated}`.
- `model.progress` [4] `{turn,total,cache,processed}`; `model.delta` [4] `{turn,kind:reasoning|content|tool_call,index,text}`.
- `model.response` [4] `{turn,finish_reason,content,reasoning_tokens,tool_calls:[{id,name,arguments}],usage:{prompt_tokens,completion_tokens,cached_tokens:null|int},timings:null|object,duration_ms}`.
- `tool.call` [4] `{turn,call_id,name,args}`; `tool.result` [4] `{turn,call_id,name,ok,ms,bytes,tokens,preview}`; `tool.toggled` [4] `{name,enabled}`.
- `message.appended` [4] `{message}`; `message.updated` [8] `{id,patch}`; `message.queued` [6] `{message_id,position}`.
- `budget` [4] uses the `Budget` shape. Prompt 4 emits a rough estimate; prompt 8 resolves exact versus estimated accounting.
- `approval.required` [6] `{call_id,name,args}`; `approval.decided` [6] `{call_id,decision}`; `cycle.detected` [6] `{call_id,name,args,prior_call_id}`; `workspace.conflict` [6] `{path,session_id,other_session_id,other_label,age_s}`.
- `compaction` [8] `{kind:elide|summarize,before,after,affected_ids,summary_message_id?}`; `memory.noted` [8] `{note,path}`.

## HTTP API

- `GET /` and `/chat` serve their page or a placeholder; `GET /static/*` serves `web/`.
- `GET /api/events` is SSE; `GET /api/state` is `snapshot.data`.
- `GET /api/sessions`; `POST /api/sessions {label?,server_id,workspace?}` → 201 `{session}`; `POST /api/sessions/{id} {label}`; `DELETE /api/sessions/{id}?force=1`; `POST /api/sessions/{id}/reset`.
- `GET /api/servers`; `POST /api/servers/{id}/probe` → 202 and an eventual event.
- `GET /api/config`; `POST /api/config` deep-merges keys and server entries by immutable `id`, validates, saves, and publishes `config.changed`. The masked API-key sentinel means unchanged. Validation errors are 400 `{error,field}`.
- `POST /api/message`, `/api/stop`, `/api/tools/{name}`, and `/api/approve` drive runs, cancellation, per-session tool toggles, and approval decisions.

## Behavior notes

- A write from one session blocks another session's `write_file` or `edit_file` until that session re-reads the shared path; the refusal identifies the other session and publishes `workspace.conflict`.
- With `run.queue_depth > 0`, messages posted during a run wait per session and start in order after it ends; `user_stop` discards the remaining messages. Depth `0` keeps the immediate 409 behavior.
- `run.cycle_window: 0` disables repeated-call cycle detection. `run.max_consecutive_tool_errors: 0` disables the consecutive tool-error stop.

## Configuration

Every parsed key appears in `harness.example.json`: `listen`, `workspace`, `log_dir`, `servers`; `run.{max_turns,cycle_window,max_consecutive_tool_errors,max_concurrent,queue_depth}`; `approval.mode`; `context.{soft_pct,summary_pct,accounting}`; `memory.{enabled,dir,max_tokens}`; `tools.{read_file,list_dir,grep}`; and `shell.{command,timeout_s,max_timeout_s,max_output_lines_head,max_output_lines_tail,deny}`. Files are saved as two-space JSON in this order. A v1 `server` document migrates to `servers[local]`.

Profiles with `tool_calls=false`, `overflow_behavior=truncate`, `streaming=false`, or unknown context are not runnable. `context.accounting=exact` later refuses profiles without `/tokenize` using `exact accounting requested but this server has no /tokenize`; `auto` chooses exact where possible and calibrated estimation elsewhere; `estimated` forces estimation.

## Deferred: API authentication

The HTTP service binds loopback and has no API authentication. Putting a token into the only practical native `EventSource` transport (`?token=`) would leak it through logs and browser history without protecting against a present network threat. Authentication must be designed when a later Discord/non-loopback transport introduces a real boundary, not added piecemeal.
