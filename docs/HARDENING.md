# Windows host hardening

These scripts reduce the reach of AgentB's model-selected shell by running the harness under a dedicated local account, denying that account writes to selected control files, and documenting a safe outbound-firewall decision. They do not jail `shell`, inspect every process it starts, or contain a determined exploit. Treat them as blast-radius reduction on a dedicated Windows host, not as a sandbox.

Run the commands below from Windows PowerShell opened with **Run as administrator**. Review every `-WhatIf` result before omitting that switch. The scripts never create or store a password; account setup prompts for it interactively only during a real creation.

## 1. Prepare the harness

Build or copy the harness binary into the repository root. Create and test `harness.json` before applying ACLs. The service identity will be denied config writes, so use a pre-probed profile with known capabilities and `probe_mode` set to `off`; runtime profile edits, full-probe saves, and settings saves will fail by design.

The harness writes its config, JSONL logs, workspace files, and memory notes during normal operation. The shell child has the same identity as the harness, so an ACL cannot let the harness append to `logs/` while denying that same access to shell commands. Log write access is preserved. Put `memory.dir` inside the workspace or disable memory; the ACL script intentionally grants write access only to the workspace.

Keep the launch environment minimal. In particular, do not place credentials in environment variables: the shell inherits and can read every variable available to AgentB.

## 2. Create the service account

Preview, then create the default `agentb-svc` account:

```powershell
.\scripts\setup-service-account.ps1 -WhatIf
.\scripts\setup-service-account.ps1
```

Use `-AccountName` consistently on all three scripts to choose another name. The script retains ordinary Users membership because removing it can break local or batch launch, ensures a newly created account is not an Administrator, and grants no interactive or Remote Desktop logon rights. Configure Task Scheduler or another service wrapper manually to run `harness.exe` under the account, granting only the logon right that mechanism requires.

Re-running account setup is an idempotent no-op when the account already exists. If that existing account is an Administrator, the script warns and leaves it unchanged; correct the membership before continuing.

## 3. Apply and verify ACLs

Stop AgentB. Preview the exact paths, apply the ACLs, and verify them:

```powershell
.\scripts\apply-acls.ps1 -WhatIf
.\scripts\apply-acls.ps1
.\scripts\apply-acls.ps1 -Verify
```

Use `-HarnessDirectory`, `-WorkspaceDirectory`, and `-MemoryDirectory` when your layout differs. The script adds explicit deny ACEs for control-surface writes while preserving inheritance and adds an explicit inheritable Modify allow ACE only to the workspace. Verification reports missing or changed managed ACEs and exits nonzero on enforceable drift. It also repeats the unavoidable shared-identity limitation for logs.

The protected surface is the harness directory entry and harness binary, `prompts/`, `harness.json`, existing `harness.*.json` files, `.git/`, `scripts/`, and `serve/`. An administrator remains able to update these paths.

## 4. Review outbound firewall policy

Preview the requested account-scoped policy and the model endpoint it must preserve:

```powershell
.\scripts\apply-firewall-rule.ps1 -ModelAddress 127.0.0.1 -ModelPort 8080 -WhatIf
```

[Windows Firewall gives an explicit Block rule precedence over a conflicting explicit Allow rule](https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/rules). Consequently, a blanket `-LocalUser` block plus a narrower model-server allow rule would block the model connection too. A true deny-by-default allow-list therefore requires either a machine-wide outbound default of Block plus carefully audited allow rules, or a different non-overlapping policy design. The script does not make that machine-wide decision and deliberately exits without creating rules in apply mode. Do not treat its refusal as active egress containment.

The [`-LocalUser` parameter](https://learn.microsoft.com/en-us/powershell/module/netsecurity/new-netfirewallrule) accepts principals as SID-based SDDL. Its value is constructed at runtime from the local account SID in the verified form `D:(A;;CC;;;<SID>)`; no machine SID is embedded in the repository. The local profiles on the development machine inherited the normal outbound-allow default, so no firewall mutation was attempted.

If a future reviewed policy creates either reserved rule name, preview and perform cleanup with:

```powershell
.\scripts\apply-firewall-rule.ps1 -Remove -WhatIf
.\scripts\apply-firewall-rule.ps1 -Remove
```

The predictable names are `AgentB-Svc-Outbound-Block` and `AgentB-Svc-Model-Allow`.

## 5. Launch and verify

Launch AgentB under the service account with only its required environment. Confirm the process owner in Task Manager or Process Explorer, exercise a workspace edit, confirm JSONL append behavior, and run:

```powershell
.\scripts\apply-acls.ps1 -Verify
Get-NetFirewallRule -Name 'AgentB-Svc-*' -ErrorAction SilentlyContinue
```

Because the firewall script intentionally declines the unsafe block-plus-allow construction, independently verify whatever outbound policy the operator chooses before relying on it.

## Rollback

Stop AgentB before rollback.

To remove the dedicated account after copying out any service-owned workspace or log data:

```powershell
Remove-LocalUser -Name 'agentb-svc'
```

To remove the dedicated identity's deny ACEs from the harness tree and its workspace grant, set the paths and qualified account name for your machine, inspect the current entries with `icacls $root`, and then remove only that identity's deny/grant entries:

```powershell
$root = (Resolve-Path '.').Path
$account = "$env:COMPUTERNAME\agentb-svc"
icacls $root /remove:d $account /t /c
icacls (Join-Path $root 'workspace') /remove:g $account /t /c
```

To remove the reserved firewall rules, use the script's `-Remove` switch shown above. It is idempotent and reports when both rules are already absent. If the operator separately changed a firewall profile's default outbound action or added other allow rules, reverse those manual changes from the recorded pre-change policy; this repository cannot safely infer or roll them back.
