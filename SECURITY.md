# Security

AgentB executes shell commands selected by a model. Use it only where that authority is acceptable.

## Workspace and shell boundary

The workspace is a boundary for the file tools only. The `shell` tool is not jailed and cannot be contained by its tool-layer wrapper: it can change directories, use absolute paths, invoke network clients, and run any program available to the harness account. As documented in [harness-plan.md §6.4](harness-plan.md#64-the-shell-tool-honestly), the wrapper pins the initial working directory, kills the process tree on timeout, checks a small configurable denylist, and logs the complete call and result. That is all. An OS sandbox is the real isolation boundary and remains deferred.

The default file-routing guard refuses simple shell shapes that duplicate `glob` or `read_file`. It is a routing aid, not a security boundary: it does not restrict what the shell can do and is not a sandbox.

The shell inherits the harness process environment. Any credential available there is readable by the agent. Run AgentB under a dedicated low-privilege account with a minimal environment, and use an OS sandbox when the workspace or model input is not fully trusted.

## Approval and unattended runs

With `approval.mode: "off"`, including during run-until-done operation, writes and shell commands execute without confirmation. The shipped default is `mutating`, which pauses `write_file`, `edit_file`, and `shell` for approval. Approval reduces accidental actions; it does not turn the shell wrapper into a sandbox.

## Network exposure

AgentB has no API authentication by design and ships with `listen` bound to loopback. Moving the listener off loopback exposes an endpoint that can run shell commands. That requires a real access design, such as a reverse proxy with TLS and strong authentication; an SSH tunnel is the recommended remote-access path. A shared secret added directly to this API is not an adequate substitute.

## Stored model content

`logs/*.jsonl` contains complete prompts and responses, including file contents and command output, and is ignored for that reason.

`memory/*.md` contains model-authored notes that are injected into future sessions' system prompts for the same workspace. A note derived from untrusted content can therefore persist across sessions. Review it—the settings sheet shows the content—and remove the note file manually if it should not be trusted or retained.
