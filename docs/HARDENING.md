# Windows host hardening

These controls reduce the reach of AgentB's model-selected shell by keeping the harness under the operator identity while launching shell children under a dedicated local account, denying that account writes to selected control files, and documenting a safe outbound-firewall decision. They do not jail `shell`, inspect every process it starts, or contain a determined exploit. Treat them as blast-radius reduction on a dedicated Windows host, not as a sandbox.

Use the Settings flow below for normal onboarding. It keeps AgentB under the ordinary operator identity and elevates only the account script through Windows UAC. Open Windows PowerShell with **Run as administrator** only for the later ACL/firewall commands or for the manual account-setup fallback. Review every `-WhatIf` result before omitting that switch.

Run every command that can prompt as a separate paste, and wait for it to finish before entering another command. The scripts now detect and drain queued console input, then abort, because a pasted following line could otherwise be consumed as a password or confirmation. Redirected/non-interactive input is also refused; there is intentionally no command-line password parameter.

## 1. Prepare AgentB

Build or copy the harness binary into the repository root. Create and test `harness.json` before applying ACLs. AgentB itself remains the operator process and can continue to save config and append logs; only model-selected shell children use the service identity.

The harness writes its config, JSONL logs, workspace files, and memory notes during normal operation. Put `memory.dir` inside the workspace or disable memory; the ACL script grants the service identity write access only to the workspace. The service identity also needs read/execute access to the selected shell executable and ordinary Windows system files.

AgentB explicitly gives alternate-identity shell children only system paths, a workspace-backed temporary directory, account identity names, and optional locale/`NO_COLOR` values. Consequently, non-system programs such as Git or a locally installed compiler require absolute paths unless the implementation's allowlist is extended deliberately. The disabled/fallback path still inherits the operator environment, so do not place credentials there.

## 2. Create the service account in Settings

Start AgentB normally by double-clicking `start-agentb.cmd`, then open Settings → Shell. Keep `account` set to `agentb-svc` and `domain` set to `.` unless you deliberately chose another local account name. Under **Local Windows account**, enter the new password twice and select **Create account + enable**. AgentB validates both entries, stores the value in its user-scoped DPAPI credential file, and asks Windows to run only `setup-service-account.ps1` through UAC. Approve the UAC prompt you just initiated.

The elevated helper creates a non-administrator account, preserves ordinary Users membership, validates the credential with `LogonUser`, and exits. AgentB then enables the service identity and immediately attempts its no-op alternate-identity spawn. A successful result is reported in the sheet. If the account already exists, the button becomes **Reset password + enable** and performs the same validation/update sequence.

Canceling UAC starts no account operation and restores the credential that was present before the attempt. If the elevated helper starts and then fails, AgentB deliberately retains the submitted credential because the password may already have changed; refresh account status, inspect the displayed failure, and use **Reset password + enable** to recover. Passwords never appear in process arguments, responses, or logs. The DPAPI blob is decrypted by the elevated helper as the same Windows operator; supplying different administrator credentials at the UAC boundary will fail safely because that other user cannot decrypt it.

The commands below remain a manual fallback. Run each prompting command alone in an elevated Windows PowerShell:

Preview, then create the default `agentb-svc` account:

```powershell
.\scripts\setup-service-account.ps1 -WhatIf
```

Run this prompting command by itself:

```powershell
.\scripts\setup-service-account.ps1
```

Use `-AccountName` consistently on all three scripts to choose another name. The script retains ordinary Users membership because removing it can break process launch, ensures a newly created account is not an Administrator, and grants no interactive or Remote Desktop logon rights. Do not configure AgentB itself to run under this account: `CreateProcessWithLogonW` launches only shell children and does not require an interactive logon right.

Re-running account setup without `-ResetPassword` leaves an existing account unchanged and prints a warning that its password is unknown and unverified. If that account is an Administrator, the script also warns; correct the membership before continuing.

If the account exists but its password is unknown, preview the recovery path first. The preview still asks for two matching entries so the complete prompt path is checked, but `-WhatIf` applies nothing and skips credential validation:

```powershell
.\scripts\setup-service-account.ps1 -ResetPassword -WhatIf
```

Then run this prompting command by itself. It asks twice, rejects empty/short/command-shaped or mismatched entries, changes only a separate non-administrator service account, and validates the resulting credential with Windows `LogonUser` before reporting success:

```powershell
.\scripts\setup-service-account.ps1 -ResetPassword
```

Without `-ResetPassword`, an existing account is only reported as an unverified warning; its password is never silently changed or treated as known.

## 3. Verify the stored credential and identity

The integrated account flow already stores the credential and enables the split. For a manually created account, open Settings → Shell, enter the service-account password in the write-only **Stored credential** field, select **Store**, enable **service identity**, and select **Test**. The UI returns only whether a credential is stored and its file timestamp; the password is encrypted with user-scoped DPAPI in `.agentb-shell-credential.dpapi` beside the selected config and never returns to the browser.

Set the account and domain (`agentb-svc` and `.` by default), enable **service identity**, then select **Test**. A successful test creates and waits for a no-op child under that identity. A missing account, bad credential, missing logon right, or process API failure is reported without revealing the password.

Before applying the remaining controls, run an approved shell call for `whoami` and confirm it returns the service account. Confirm the process owner externally with Task Manager or Process Explorer too. A red **SHELL IDENTITY FALLBACK** banner means shell is running as the operator; stop and correct its stated cause. The alarm clears only after an alternate-identity spawn actually succeeds.

The ACL and firewall controls below do nothing useful until this split is enabled and verified.

## 4. Apply and verify ACLs

Stop AgentB. Preview the exact paths, apply the ACLs, and verify them:

```powershell
.\scripts\apply-acls.ps1 -WhatIf
```

The real application uses PowerShell confirmations; run it by itself:

```powershell
.\scripts\apply-acls.ps1
```

After it finishes, verify separately:

```powershell
.\scripts\apply-acls.ps1 -Verify
```

Use `-HarnessDirectory`, `-WorkspaceDirectory`, and `-MemoryDirectory` when your layout differs. The script adds explicit deny ACEs for control-surface writes while preserving inheritance and adds an explicit inheritable Modify allow ACE only to the workspace. Verification reports missing or changed managed ACEs and exits nonzero on enforceable drift. The current Prompt 16 policy still leaves `logs/` and `memory/` unmanaged; it now reports that limitation without repeating the obsolete shared-identity claim.

The protected surface is the harness directory entry and harness binary, `prompts/`, `harness.json`, existing `harness.*.json` files, `.git/`, `scripts/`, and `serve/`. An administrator remains able to update these paths.

## 5. Review outbound firewall policy

Preview the requested account-scoped policy and the model endpoint it must preserve:

```powershell
.\scripts\apply-firewall-rule.ps1 -ModelAddress 127.0.0.1 -ModelPort 8080 -WhatIf
```

[Windows Firewall gives an explicit Block rule precedence over a conflicting explicit Allow rule](https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/rules). Consequently, a blanket `-LocalUser` block plus a narrower model-server allow rule would block the model connection too. A true deny-by-default allow-list therefore requires either a machine-wide outbound default of Block plus carefully audited allow rules, or a different non-overlapping policy design. The script does not make that machine-wide decision and deliberately exits without creating rules in apply mode. Do not treat its refusal as active egress containment.

The [`-LocalUser` parameter](https://learn.microsoft.com/en-us/powershell/module/netsecurity/new-netfirewallrule) accepts principals as SID-based SDDL. Its value is constructed at runtime from the local account SID in the verified form `D:(A;;CC;;;<SID>)`; no machine SID is embedded in the repository. The local profiles on the development machine inherited the normal outbound-allow default, so no firewall mutation was attempted.

If a future reviewed policy creates either reserved rule name, preview and perform cleanup with:

```powershell
.\scripts\apply-firewall-rule.ps1 -Remove -WhatIf
```

Run the potentially prompting removal by itself:

```powershell
.\scripts\apply-firewall-rule.ps1 -Remove
```

The predictable names are `AgentB-Svc-Outbound-Block` and `AgentB-Svc-Model-Allow`.

## 6. Launch and verify

Keep AgentB running under the operator identity. Confirm shell children are owned by the service account, exercise a shell workspace edit, confirm harness JSONL append behavior, and run:

```powershell
.\scripts\apply-acls.ps1 -Verify
Get-NetFirewallRule -Name 'AgentB-Svc-*' -ErrorAction SilentlyContinue
```

Because the firewall script intentionally declines the unsafe block-plus-allow construction, independently verify whatever outbound policy the operator chooses before relying on it.

## Rollback

Stop AgentB before rollback.

First disable `shell.service_account.enabled` in Settings. Remove the stored credential with Settings → Shell → **Clear**. To remove the dedicated account after copying out any service-owned workspace data:

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
