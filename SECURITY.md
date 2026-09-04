# Security

Agent_b executes shell commands selected by a model. Use it only where that authority is acceptable.

## Workspace and shell boundary

With the service identity disabled, the workspace remains the boundary for file tools. With it enabled, built-in file tools impersonate the configured low-privilege Windows account and accept absolute paths; Windows ACLs then decide which paths they can read or modify. The `shell` tool is not jailed and cannot be contained by its tool-layer wrapper: it can change directories, use absolute paths, invoke network clients, and run any program available to its service-account token. As documented in [harness-plan.md §6.4](harness-plan.md#64-the-shell-tool-honestly), the wrapper pins the initial working directory, kills the process tree on timeout, checks a small configurable denylist, and logs the complete call and result. [Windows host hardening](docs/HARDENING.md) reduces blast radius; it is not an OS sandbox.

The default file-routing guard refuses simple shell shapes that duplicate `glob` or `read_file`. It is a routing aid, not a security boundary: it does not restrict what the shell can do and is not a sandbox.

Settings → Security → **run tools as me** is an explicit capability escape hatch. It bypasses service-account isolation for shell and file tools, including the file tools' workspace boundary, and leaves access decisions to the Windows account that launched Agent_b. A persistent red banner announces the mode. It does not elevate that account or bypass Windows ACLs.

When `shell.service_account.enabled` is false, the shell inherits the Agent_b process identity and environment as before. When it is true and the stored credential works, Agent_b creates shell processes under the configured low-privilege Windows identity with an explicit minimal environment. Its `PATH` contains only Windows system directories and the Windows PowerShell directory, so other tools require absolute paths. A failed alternate-identity launch does not execute the command: it logs the reason, raises a persistent red UI alarm, and requires the user to approve an exact-command operator retry. The alternate path preserves the initial working directory, combined output, timeout, and process-tree termination through a Windows job object, but it still requires an operator-machine test before being relied upon. A separate account limits reach; it is not a sandbox and does not contain an exploit. Follow [Windows host hardening](docs/HARDENING.md) before relying on the split.

Settings can launch the account setup script as a hidden elevated helper through Windows UAC while Agent_b remains non-elevated. The two password entries are write-only, are never placed on a command line or in logs, and cross the elevation boundary only as the existing user-scoped DPAPI blob. Keep the service on loopback, approve UAC only for an operation you initiated, and treat any failure after the elevated helper starts as a potentially partial account/password change.

Agent_b refuses to start when its effective Windows token is elevated, before loading configuration, probing a model, opening the listener, or enabling shell execution. Membership in the local Administrators group is allowed: a normal Explorer launch uses the UAC-filtered token. This keeps **Run once as operator** from inheriting Administrator authority.

Settings can also apply and verify complete-tree service-account ACLs and a single account-scoped outbound Block rule through UAC. The ACL policy excludes only the configured workspace and must be reapplied after application updates create or replace files. The firewall policy permits loopback and the complete Tailscale carrier range; it does not change machine-wide firewall defaults or inspect destinations above the IP layer.

The Windows installer is per-user and does not elevate. On first install it can copy the current checkout's local configuration and user-scoped DPAPI credential into the installation directory without displaying their contents; upgrades leave installed data in place. The uninstaller can remove or preserve that local data, but it does not silently delete the service account, ACL policy, or firewall rule because those machine-level controls may be shared.

## Approval and unattended runs

With `approval.mode: "off"`, including during run-until-done operation, writes and shell commands execute without confirmation. The shipped default is `mutating`, which pauses `write_file`, `edit_file`, and `shell` for approval. Approval reduces accidental actions; it does not turn the shell wrapper into a sandbox.

When Windows denies a built-in file operation, or shell output looks like an OS permission denial, Agent_b offers a separate **Run once as operator** decision. This gate is mandatory even when `approval.mode` is `off`; the model cannot invoke the operator path directly, the UI shows the exact path or command, and the decision is logged. Approval reruns only that call once with the non-elevated Agent_b process identity. File-tool denial comes from the impersonated Windows token; shell denial classification remains a text heuristic. Inspect the operation before approving: this escape hatch deliberately bypasses service-account ACL and firewall restrictions once and is not a security boundary.

## Network exposure

Agent_b has no user authentication and ships with `listen` bound to loopback. Each launch generates an unguessable mutation token delivered through the same-origin state stream; every non-read request requires that token, cross-origin browser mutations are rejected, framing is denied, and a restrictive Content Security Policy is sent. This is a browser/CSRF barrier, not authentication against another process running as the operator. Moving the listener off loopback exposes an endpoint that can run shell commands and requires a real TLS/authentication design.

## Stored model content

`logs/*.jsonl` contains complete prompts and responses, including file contents and command output, and is ignored for that reason.

`memory/*.md` contains model-authored notes that are injected into future sessions' system prompts for the same workspace. A note derived from untrusted content can therefore persist across sessions. Review it—the settings sheet shows the content—and remove the note file manually if it should not be trusted or retained.
