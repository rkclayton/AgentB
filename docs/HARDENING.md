# Windows host hardening

These controls reduce the reach of Agent_b's model-selected shell. Shell children run under a dedicated local account, that account can write only the selected workspace inside the Agent_b tree, and its outbound traffic is limited to IPv4 loopback, Tailscale's `100.64.0.0/10`, and IPv6 loopback. This is blast-radius reduction on a dedicated Windows host, not a sandbox or exploit boundary.

The normal onboarding path is entirely in Settings → Security. Start Agent_b normally from Explorer so it receives the UAC-filtered token; local Administrators can use this path normally. Agent_b refuses to start from **Run as administrator** or an elevated terminal, before it reads configuration or opens its listener. Windows elevation is reserved for the narrowly scoped account and hardening helpers. Passwords are write-only, encrypted with user-scoped DPAPI, and never placed in process arguments, responses, or logs.

## 1. Configure the model first

Double-click `start-Agent_b.cmd`. In Settings → Connections, configure and test the model endpoint, then select the ready profile for the session. Host protection accepts only a numeric address inside `127.0.0.0/8`, `100.64.0.0/10`, or IPv6 loopback because every other destination will be blocked for the service identity.

Agent_b gives alternate-identity shell children only Windows system paths, a workspace-backed temporary directory, account identity names, and optional locale/`NO_COLOR` values. Git, compilers, and other non-system programs therefore need absolute executable paths unless the environment allowlist is deliberately extended.

## 2. Create and verify the service identity

In Settings → Security, enter a new password twice under **Service identity**, select **Create account**, and respond if Windows presents a UAC prompt. The default local account is `agentb-svc`; its advanced account and domain fields are available only when a different identity is intentional.

The helper creates a non-administrator local account, retains ordinary Users membership, validates the credential with Windows, stores the DPAPI credential, enables the identity split, and tests a no-op alternate-identity process. If the account already exists, the button becomes **Reset password**.

Canceling UAC starts no account operation and restores the previous stored credential. A failure after the elevated helper starts is reported as potentially partial; use **Reset password + enable** to recover. Supplying different administrator credentials at UAC will fail safely because another Windows user cannot decrypt the operator-scoped DPAPI blob.

Run an approved `whoami` shell command and require the returned identity to end in `\agentb-svc`. Confirm the process owner externally with Task Manager or Process Explorer. If alternate-identity spawning fails, Agent_b does not run the command as the operator: it raises a persistent red alarm and requires a separate **Run once as operator** decision.

## 3. Apply host protections

Stop active Agent_b tasks. In Settings → Security → **Host protections**, confirm the displayed model route, select **Apply + verify**, and respond if Windows presents a UAC prompt. The button remains disabled until the account exists, the credential is stored, and service identity is enabled. Local UAC policy can suppress consent for trusted Windows binaries, so trust the reported post-condition rather than the presence of a dialog.

The operation applies and immediately verifies both controls:

- Every existing top-level file and directory in the Agent_b application tree receives an explicit service-account write/delete/ownership deny, recursively for directories. This includes the binary, source, UI, prompts, configuration, scripts, `.git`, logs, memory, and local toolchains. Read and execute remain available.
- The selected workspace receives an explicit recursive `Modify` grant. If it is inside the Agent_b directory, it must be one direct child so it can be excluded cleanly from the recursive denies.
- One outbound Block rule named `AgentB-Svc-Outbound-Block` is scoped to the service account with `-LocalUser`. Non-overlapping address ranges spare `127.0.0.0/8`, `100.64.0.0/10`, and `::1`. There is no competing Allow rule and no machine-wide `DefaultOutboundAction` change.

Select **Verify** at any time to detect missing or replaced ACL entries and firewall drift. Reapply after updating, rebuilding, or adding files to Agent_b because a newly created or replaced application file may not retain its explicit deny.

The network rule permits every loopback and Tailscale destination, not only the model server. It prevents ordinary public/LAN egress by this Windows identity; it is not a domain allowlist, protocol inspection, or protection against a kernel-level exploit.

## 4. RBAC demonstration checks

Run these through Agent_b after **Apply + verify**:

1. `whoami` returns the service account.
2. Creating and deleting a file in the workspace succeeds.
3. Creating a file under the sibling `web` directory reports a permission denial.
4. Choose **Keep denied** and confirm no operator retry occurs.
5. Repeat, choose **Run once as operator**, and confirm the exact command succeeds only once. This path runs as the account that launched Agent_b and inherits its environment. Keep Agent_b non-elevated so this override does not acquire Administrator authority.
6. A connection to the configured loopback or Tailscale model endpoint succeeds.
7. A direct connection to a public test address fails under the service identity.
8. A timed-out command loses its complete process tree.
9. The timeline/log contains the original command, the approval decision, and the operator retry result.

The permission classifier reads command output heuristically. A model can print denial-looking text, so the prompt is not proof of an OS decision: always inspect the exact displayed command.

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

After storing/testing that credential in Settings, preview, apply, and verify ACLs:

```powershell
.\scripts\apply-acls.ps1 -WhatIf
```

```powershell
.\scripts\apply-acls.ps1
```

```powershell
.\scripts\apply-acls.ps1 -Verify
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

Stop active tasks, disable `shell.service_account.enabled`, then select **Remove** twice under Host protections and approve UAC. Clear the stored credential afterward. Remove the local account only after the ACL and firewall removal verifies successfully and any service-owned workspace data has been copied out.

Manual rollback uses the scripts before deleting the account:

```powershell
.\scripts\apply-firewall-rule.ps1 -Remove
```

```powershell
.\scripts\apply-acls.ps1 -Remove
```

```powershell
Remove-LocalUser -Name 'agentb-svc'
```
