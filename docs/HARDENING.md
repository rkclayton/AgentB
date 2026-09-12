# Windows host hardening

These controls reduce the reach of Agent_b's model-selected OS operations. Shell children and built-in file tools use a dedicated local account. File-tool paths rely on that identity's ACLs; shell is never workspace-confined, and its workspace is only the initial working directory. Absolute paths and `cd ..` therefore remain possible wherever Windows grants the active account access. The managed Agent_b tree grants writes to its workspace and configured exchange folder only, and service-account outbound traffic is limited to IPv4 loopback, Tailscale's `100.64.0.0/10`, and IPv6 loopback. This is blast-radius reduction on a dedicated Windows host, not a sandbox or exploit boundary.

Because ACLs constrain shell reach rather than behavior, `run_script` requires policy confirmation while the service identity is enabled, regardless of `approval.mode`. Ordinary `shell` follows the configured mode: `boundary-only`/`off` are silent until an identity or permission denial raises **Run as you**, while `mutating`/`all` use **Allow this** before service execution. Configured `tools.shell.operator_commands` are resolved from the command's first executable, never run as the service account, and require **Run as you**. **Yes, for this chat** covers every later shell, file, or interpreter identity need in that chat; **Just once** covers only the displayed operation. With the service identity disabled, no posture-specific identity gate applies. In that unrestricted posture, `internal/tools/jail.go` remains the sole workspace constraint for built-in file tools and must not be removed; it does not apply to shell.

For a deliberate temporary capability override, Settings → Security offers **Run everything as me for 20 minutes**. It runs shell and file tools as the non-elevated Windows account that launched Agent_b and permits OS-authorized paths outside the workspace, defeating the service-account ACL boundary by design while on. The Settings robot and Chat status-strip expiry follow observed server state, including failed grants and idle expiry. The mode is off after every start and lapses after 20 minutes without agent tool activity by default. Each ordinary tool execution resets the idle deadline at start and completion; browser activity and one-shot ACL escapes do not. There is no absolute ceiling, but a single call running beyond the idle window is allowed to lapse so a wedge cannot leave the grant open indefinitely. Turn it off sooner in Settings to restore the service identity; this mode does not grant Administrator privileges or bypass Windows ACLs.

The installer uses a same-user UAC prompt to write the application under Program Files and refuses over-the-shoulder elevation by another administrator. The installed Agent_b process itself must still start normally from Explorer with a UAC-filtered token. Agent_b refuses to run from **Run as administrator** or an elevated terminal, before it reads configuration or opens its listener. Later Windows elevation is reserved for the narrowly scoped account and hardening helpers. Passwords are write-only, encrypted with user-scoped DPAPI, and never placed in process arguments, responses, or logs.

## Recover an absent production baseline

An absent listener is not automatically a crash and an empty event query is not proof of a clean shutdown. Before any recovery start, preserve and inspect `%LOCALAPPDATA%\Agent_b\logs\launcher-errors.log`, the newest `installer-*.log`, and Application Error / Windows Error Reporting events naming `Agent_b.exe`. Installer transcripts explicitly record `STOPPING` and `STOPPED`; foreground launcher failures record their nonzero exit, while detached launcher stderr is not durable.

If any source positively reports abnormal termination, stop and diagnose it. Otherwise a routine but unexplained absence permits one start through the installed `Agent_b.cmd` launcher. Verify the new PID and the build reported by `/api/state`. If that process exits or never becomes ready, preserve its exit code and diagnostics and stop—never start it again automatically. Even when the one start succeeds, keep the prior absence classified as unexplained unless positive evidence identified its cause. This procedure never permits stopping a running instance and never applies to alpha.

## 1. Configure the model first

For an installed copy, open **Agent_b** from Start. For a source checkout, double-click `start-Agent_b.cmd`. In Settings → Connections, configure and test the model endpoint, then select the ready profile for the session. Host protection accepts only a numeric address inside `127.0.0.0/8`, `100.64.0.0/10`, or IPv6 loopback because every other destination will be blocked for the service identity.

Agent_b gives alternate-identity shell children only Windows system paths, a workspace-backed temporary directory, account identity names, and optional locale/`NO_COLOR` values. Git, compilers, and other non-system programs therefore need absolute executable paths unless the environment allowlist is deliberately extended.

The setup guide's interpreter list reports the resolved executable path and whether its ACL is readable/executable by the service identity. It does not broaden the service `PATH`. Install an interpreter for all users under Program Files when both identities need it, then invoke it by absolute path and verify the real service-context command.

## 2. Create and verify the service identity

In Settings → Security, enter a new password twice under **Service identity**, select **Create account**, and respond if Windows presents a UAC prompt. The default local account is `agentb-svc`; its advanced account and domain fields are available only when a different identity is intentional.

The helper creates a non-administrator local account, retains ordinary Users membership, validates the credential with Windows, stores the DPAPI credential, and enables the identity split. If the account already exists, the button becomes **Reset password**. The real shell test follows host protection because the ACL operation grants the service account access to the configured workspace.

Canceling UAC starts no account operation and restores the previous stored credential. A failure after the elevated helper starts is reported as potentially partial; use **Reset password + enable** to recover. Supplying different administrator credentials at UAC will fail safely because another Windows user cannot decrypt the operator-scoped DPAPI blob.

Run an approved `whoami` shell command and require the returned identity to end in `\agentb-svc`. Confirm the process owner externally with Task Manager or Process Explorer. If alternate-identity spawning fails, Agent_b does not silently run the command as the operator: it returns the reason and requires **Run as you**.

## 3. Apply host protections

Stop active Agent_b tasks. Choose the exchange path and delivery mode in Settings → Delivery and save them. Then, in Settings → Security → **Host protections**, confirm the displayed model route, select **Apply protection**, and respond if Windows presents a UAC prompt. **Save** stores configuration; **Apply protection** changes Windows policy; **Verify** is read-only. The Apply operation grants workspace and exchange-folder access before testing the real service-account shell, avoiding a circular first-run dependency. The button remains disabled until the account exists, the credential is stored, and service identity is enabled. Local UAC policy can suppress consent for trusted Windows binaries, so trust the reported post-condition rather than the presence of a dialog.

The operation applies and immediately verifies both controls:

- The Program Files application root receives a recursive service-account read/execute grant plus explicit write/delete/ownership denies. The installed binary, web assets, prompts, scripts, documentation, and config template are readable but immutable to the constrained identity.
- The LocalAppData data root receives a recursive service-account `FullControl` deny. Configuration, the DPAPI credential, logs, and memory remain available only to the operator identity; the service account does not need config access.
- Parent directories receive only the traverse grants needed to reach the Program Files application, ProgramData workspace, and configured exchange folder. The workspace and exchange folder each receive an explicit recursive `Modify` grant. Application, data, workspace, and exchange trees must be disjoint; no other operator-profile content is granted read or list access.
- One outbound Block rule named `AgentB-Svc-Outbound-Block` is scoped to the service account with `-LocalUser`. Non-overlapping address ranges spare `127.0.0.0/8`, `100.64.0.0/10`, and `::1`. There is no competing Allow rule and no machine-wide `DefaultOutboundAction` change.

Select **Verify** at any time to detect missing or replaced ACL entries and firewall drift. Reapply after updating, rebuilding, or adding files to Agent_b because a newly created or replaced application file may not retain its explicit deny.

The elevated installer preserves existing settings, state, workspace contents, and protected top-level ACLs during upgrades. After the first installed launch, use **Apply protection** for that installed layout even if a source checkout was already hardened; continue to Verify after upgrades because a release that adds a new application artifact still needs the recursive policy verified.

The network rule permits every loopback and Tailscale destination, not only the model server. It prevents ordinary public/LAN egress by this Windows identity; it is not a domain allowlist, protocol inspection, or protection against a kernel-level exploit.

## 4. RBAC demonstration checks

Run these through Agent_b after **Apply protection** succeeds:

1. Approve the shell policy confirmation; `whoami` returns the service account and the result reports `operator_context:false`.
2. With the shipped `boundary-only` approval mode, creating and deleting a file in the workspace succeeds without a generic confirmation.
3. `list_dir` on an absolute path that Windows allows for the service account succeeds outside the workspace.
4. Creating a file under the sibling `web` directory reports a permission denial.
5. Choose **Keep denied** and confirm no operator retry occurs.
6. Repeat, choose **Just once** on **Run as you**, and confirm the exact operation succeeds only once. Then choose **Yes, for this chat** and confirm a file escape, shell command, and operator-only interpreter all share that one chat grant. This path runs as the account that launched Agent_b and inherits its permissions. Keep Agent_b non-elevated so this override does not acquire Administrator authority.
7. A connection to the configured loopback or Tailscale model endpoint succeeds.
8. A direct connection to a public test address fails under the service identity.
9. A timed-out command loses its complete process tree.
10. The timeline/log distinguishes the original operation from the mandatory boundary-escape decision and the operator retry result.

File-tool denials come directly from Windows while Agent_b is impersonating the service account. Shell permission classification reads command output heuristically, so a shell prompt is not proof of an OS decision: always inspect the exact displayed operation.

## Manual script fallback

Run prompting commands one at a time. The scripts detect and reject buffered or redirected input because a pasted following line could otherwise be consumed as a password or confirmation.

Account preview and creation:

```powershell
.\scripts\setup-service-account.ps1 -WhatIf
```

```powershell
.\scripts\setup-service-account.ps1
```

Recover an existing account whose password is unknown:

```powershell
.\scripts\setup-service-account.ps1 -ResetPassword
```

After storing/testing that credential in Settings, preview, apply, and verify ACLs. Substitute the same four explicit roots for each invocation:

```powershell
.\scripts\apply-acls.ps1 -ApplicationDirectory "$env:ProgramFiles\Agent_b" -DataDirectory "$env:LOCALAPPDATA\Agent_b" -WorkspaceDirectory "$env:ProgramData\Agent_b\workspace" -ExchangeDirectory "$env:USERPROFILE\Agent_b" -WhatIf
```

```powershell
.\scripts\apply-acls.ps1 -ApplicationDirectory "$env:ProgramFiles\Agent_b" -DataDirectory "$env:LOCALAPPDATA\Agent_b" -WorkspaceDirectory "$env:ProgramData\Agent_b\workspace" -ExchangeDirectory "$env:USERPROFILE\Agent_b"
```

```powershell
.\scripts\apply-acls.ps1 -ApplicationDirectory "$env:ProgramFiles\Agent_b" -DataDirectory "$env:LOCALAPPDATA\Agent_b" -WorkspaceDirectory "$env:ProgramData\Agent_b\workspace" -ExchangeDirectory "$env:USERPROFILE\Agent_b" -Verify
```

Preview, apply, and verify the firewall rule, substituting the numeric model address and port:

```powershell
.\scripts\apply-firewall-rule.ps1 -ModelAddress 127.0.0.1 -ModelPort 8080 -WhatIf
```

```powershell
.\scripts\apply-firewall-rule.ps1 -ModelAddress 127.0.0.1 -ModelPort 8080
```

```powershell
.\scripts\apply-firewall-rule.ps1 -ModelAddress 127.0.0.1 -ModelPort 8080 -Verify
```

## Rollback

Stop active tasks, disable `shell.service_account.enabled`, then select **Remove** twice under Host protections and approve UAC. Clear the stored credential afterward. Remove the local account only after the ACL and firewall removal verifies successfully and any service-owned workspace data has been copied out. Windows Installed apps removes the application, shortcut, and registration but deliberately leaves these machine-level controls because another checkout or installation may share them.

Manual rollback uses the scripts before deleting the account:

```powershell
.\scripts\apply-firewall-rule.ps1 -Remove
```

```powershell
.\scripts\apply-acls.ps1 -ApplicationDirectory "$env:ProgramFiles\Agent_b" -DataDirectory "$env:LOCALAPPDATA\Agent_b" -WorkspaceDirectory "$env:ProgramData\Agent_b\workspace" -ExchangeDirectory "$env:USERPROFILE\Agent_b" -Remove
```

```powershell
Remove-LocalUser -Name 'agentb-svc'
```
