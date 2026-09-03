[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',

    [switch]$ResetPassword,

    # Internal web-onboarding path. The file contains a user-scoped DPAPI blob,
    # never plaintext; its path is safe to pass across the UAC boundary.
    [string]$CredentialStore,

    [switch]$Inspect,

	[switch]$ValidateCredentialStore,

	# Internal UAC helper path. The browser already supplied the two matching
	# password entries and the API validated the requested operation.
	[switch]$NoPrompt
)

$ErrorActionPreference = 'Stop'
$minimumPasswordLength = 14
$script:changeState = 'nothing'
$script:confirmationSuppressed = $NoPrompt -or ($PSBoundParameters.ContainsKey('Confirm') -and -not [bool]$PSBoundParameters['Confirm'])
if ($NoPrompt) { $ConfirmPreference = 'None' }

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-BuiltinGroupName {
    param([Security.Principal.WellKnownSidType]$SidType)
    $sid = [Security.Principal.SecurityIdentifier]::new($SidType, $null)
    return $sid.Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
}

function Write-SetupSummary {
    param([string[]]$Changed, [string[]]$NotChanged, [string[]]$Next)
    Write-Host ''
    Write-Host 'SUMMARY'
    Write-Host "Changed: $(if ($Changed.Count) { $Changed -join '; ' } else { 'nothing' })"
    Write-Host "Not changed: $(if ($NotChanged.Count) { $NotChanged -join '; ' } else { 'nothing' })"
    Write-Host "Next: $(if ($Next.Count) { $Next -join '; ' } else { 'no further action requested' })"
}

function Stop-UnsafePrompt {
    param([string]$Message, [string]$NonInteractiveParameter)
    [Console]::Error.WriteLine("PROMPT REFUSED: $Message")
    if ($NonInteractiveParameter) {
        [Console]::Error.WriteLine("For an intentional non-interactive confirmation, use $NonInteractiveParameter.")
    } else {
        [Console]::Error.WriteLine('No non-interactive password parameter is supported. Run this script by itself in an interactive console.')
    }
    Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('run this prompting command alone in an interactive console')
    exit 2
}

function Assert-SafeInteractiveInput {
    param([string]$NonInteractiveParameter = '')
    $redirected = $true
    try { $redirected = [Console]::IsInputRedirected } catch {
        Stop-UnsafePrompt -Message 'a usable console input stream could not be established.' -NonInteractiveParameter $NonInteractiveParameter
    }
    if ($redirected -or $Host.Name -ne 'ConsoleHost') {
        Stop-UnsafePrompt -Message 'input is redirected or the current PowerShell host has no usable interactive console.' -NonInteractiveParameter $NonInteractiveParameter
    }
    $buffered = $false
    try {
        while ([Console]::KeyAvailable) {
            $buffered = $true
            $null = [Console]::ReadKey($true)
        }
    } catch {
        Stop-UnsafePrompt -Message 'console input availability could not be verified safely.' -NonInteractiveParameter $NonInteractiveParameter
    }
    if ($buffered) {
        Stop-UnsafePrompt -Message 'queued console input was detected and drained. This commonly happens when a multi-line command block is pasted; run this prompting command alone.' -NonInteractiveParameter $NonInteractiveParameter
    }
}

function Test-ConfirmationPromptExpected {
    param([Management.Automation.ConfirmImpact]$Impact)
    return (-not $WhatIfPreference -and
        -not $script:confirmationSuppressed -and
        $ConfirmPreference -ne [Management.Automation.ConfirmImpact]::None -and
        [int]$Impact -ge [int]$ConfirmPreference)
}

function Test-SecureStringPrefix {
    param([Security.SecureString]$Value, [string[]]$Prefixes)
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Value)
    try {
        foreach ($prefix in $Prefixes) {
            if ($Value.Length -lt $prefix.Length) { continue }
            $matches = $true
            for ($index = 0; $index -lt $prefix.Length; $index++) {
                $actual = [char][Runtime.InteropServices.Marshal]::ReadInt16($pointer, $index * 2)
                if ([char]::ToUpperInvariant($actual) -ne [char]::ToUpperInvariant($prefix[$index])) {
                    $matches = $false
                    break
                }
            }
            if ($matches) { return $true }
        }
        return $false
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function Test-SecureStringEqual {
    param([Security.SecureString]$Left, [Security.SecureString]$Right)
    if ($Left.Length -ne $Right.Length) { return $false }
    $leftPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Left)
    $rightPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Right)
    try {
        for ($index = 0; $index -lt $Left.Length; $index++) {
            if ([Runtime.InteropServices.Marshal]::ReadInt16($leftPointer, $index * 2) -ne
                [Runtime.InteropServices.Marshal]::ReadInt16($rightPointer, $index * 2)) { return $false }
        }
        return $true
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($leftPointer)
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($rightPointer)
    }
}

function Test-PasswordShape {
    param([Security.SecureString]$Value)
    if ($Value.Length -lt $minimumPasswordLength) {
        [Console]::Error.WriteLine("PASSWORD REFUSED: the password must contain at least $minimumPasswordLength characters.")
        return $false
    }
    # This is only a heuristic against pasted commands, not password-strength validation.
    if (Test-SecureStringPrefix -Value $Value -Prefixes @('Get-', 'Set-', 'New-', '.\', 'cd ', 'git ')) {
        [Console]::Error.WriteLine('PASSWORD REFUSED: the value looks like a pasted command, not a password.')
        return $false
    }
    return $true
}

function Read-VerifiedPassword {
    param([string]$QualifiedName)
    if ($CredentialStore) {
        if (-not [IO.Path]::IsPathRooted($CredentialStore) -or -not (Test-Path -LiteralPath $CredentialStore -PathType Leaf)) {
            [Console]::Error.WriteLine('PASSWORD REFUSED: the Agent_b credential store is missing or is not an absolute file path.')
            return $null
        }
        $protected = [IO.File]::ReadAllBytes($CredentialStore)
        $plain = $null
        $characters = $null
        try {
            Add-Type -AssemblyName System.Security
            $plain = [Security.Cryptography.ProtectedData]::Unprotect(
                $protected,
                $null,
                [Security.Cryptography.DataProtectionScope]::CurrentUser
            )
            $characters = [Text.Encoding]::UTF8.GetChars($plain)
            $stored = [Security.SecureString]::new()
            foreach ($character in $characters) { $stored.AppendChar($character) }
            $stored.MakeReadOnly()
            if (-not (Test-PasswordShape -Value $stored)) {
                $stored.Dispose()
                return $null
            }
            # The browser and Go handler require two matching entries before
            # this UAC-only path is launched. The encrypted blob carries the
            # already-confirmed value without putting it on a command line.
            return $stored
        } catch {
            [Console]::Error.WriteLine("PASSWORD REFUSED: the Agent_b credential store could not be decrypted for this Windows user ($($_.Exception.Message)).")
            return $null
        } finally {
            if ($characters) { [Array]::Clear($characters, 0, $characters.Length) }
            if ($plain) { [Array]::Clear($plain, 0, $plain.Length) }
            if ($protected) { [Array]::Clear($protected, 0, $protected.Length) }
        }
    }
    Assert-SafeInteractiveInput
    $first = Read-Host "Enter the password for $QualifiedName" -AsSecureString
    if (-not (Test-PasswordShape -Value $first)) {
        $first.Dispose()
        return $null
    }
    Assert-SafeInteractiveInput
    $second = Read-Host "Enter the password for $QualifiedName again" -AsSecureString
    try {
        if (-not (Test-PasswordShape -Value $second)) {
            $first.Dispose()
            return $null
        }
        if (-not (Test-SecureStringEqual -Left $first -Right $second)) {
            [Console]::Error.WriteLine('PASSWORD REFUSED: the two entries do not match; no account change was attempted.')
            $first.Dispose()
            return $null
        }
    } finally {
        $second.Dispose()
    }
    return $first
}

function Test-LocalCredential {
    param([string]$Name, [Security.SecureString]$Password)
    if (-not ('AgentBNativeLogon' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class AgentBNativeLogon
{
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool LogonUser(string username, string domain, IntPtr password, int logonType, int logonProvider, out IntPtr token);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool CloseHandle(IntPtr handle);
}
'@
    }
    $passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToGlobalAllocUnicode($Password)
    $token = [IntPtr]::Zero
    try {
        $valid = [AgentBNativeLogon]::LogonUser($Name, '.', $passwordPointer, 2, 0, [ref]$token)
        if ($valid) {
            return [pscustomobject]@{ Valid = $true; ErrorCode = 0; Message = 'interactive credential validation succeeded' }
        }
        $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
        $message = [ComponentModel.Win32Exception]::new($errorCode).Message
        return [pscustomobject]@{ Valid = $false; ErrorCode = $errorCode; Message = $message }
    } finally {
        if ($token -ne [IntPtr]::Zero) { $null = [AgentBNativeLogon]::CloseHandle($token) }
        [Runtime.InteropServices.Marshal]::ZeroFreeGlobalAllocUnicode($passwordPointer)
    }
}

trap {
    [Console]::Error.WriteLine("SETUP FAILED: $($_.Exception.Message)")
    Write-SetupSummary -Changed @($script:changeState) -NotChanged @('no additional change is claimed after the failure') -Next @('inspect the account state before retrying; use -ResetPassword if the account now exists')
    exit 1
}

if ($Inspect) {
    if ($CredentialStore -or $ResetPassword -or $ValidateCredentialStore) {
        [Console]::Error.WriteLine('INSPECTION FAILED: -Inspect cannot be combined with credential or mutation options.')
        exit 1
    }
    $inspected = Get-LocalUser -Name $AccountName -ErrorAction SilentlyContinue
    $isAdministrator = $false
    if ($inspected) {
        $administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
        $isAdministrator = [bool](Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue |
            Where-Object { $_.SID -eq $inspected.SID })
    }
    $status = [ordered]@{
        supported = $true
        account = $AccountName
        exists = [bool]$inspected
        enabled = [bool]($inspected -and $inspected.Enabled)
        administrator = $isAdministrator
        harness_elevated = (Test-IsAdministrator)
    }
    Write-Output "AGENTB_ACCOUNT_STATUS=$($status | ConvertTo-Json -Compress)"
    exit 0
}

if ($ValidateCredentialStore) {
    if (-not $CredentialStore -or $ResetPassword) {
        [Console]::Error.WriteLine('CREDENTIAL CHECK FAILED: supply only -CredentialStore with -ValidateCredentialStore.')
        exit 1
    }
    $checkedPassword = Read-VerifiedPassword -QualifiedName "$env:COMPUTERNAME\$AccountName"
    if (-not $checkedPassword) { exit 2 }
    $checkedPassword.Dispose()
    Write-Output 'AGENTB_CREDENTIAL_STORE_VALID'
    exit 0
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
    Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('reopen PowerShell as Administrator and rerun this script alone')
    exit 1
}

$qualifiedName = "$env:COMPUTERNAME\$AccountName"
$existing = Get-LocalUser -Name $AccountName -ErrorAction SilentlyContinue
Write-Host 'Agent_b service-account setup'
Write-Host "Account: $qualifiedName"

if ($existing -and -not $ResetPassword) {
    Write-Warning 'The local account already exists. Its password is unknown to this script and was NOT changed or verified.'
    $administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
    $isAdministrator = Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue |
        Where-Object { $_.SID -eq $existing.SID }
    if ($isAdministrator) {
        Write-Warning 'The existing account is an Administrator. Remove that membership before using it for Agent_b.'
    }
    Write-SetupSummary -Changed @() -NotChanged @('existing account', 'password', 'group memberships', 'logon rights') -Next @('run this script alone with -ResetPassword to set and verify a known password', 'then store and test it in Agent_b Settings')
    exit 0
}

if (-not $existing -and $ResetPassword) {
    Write-Warning 'The requested password reset cannot run because the local account does not exist.'
    Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('rerun this script alone without -ResetPassword to create the account')
    exit 1
}

if ($existing -and $ResetPassword) {
    $administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
    $isAdministrator = Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue |
        Where-Object { $_.SID -eq $existing.SID }
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    if ($isAdministrator -or $existing.SID -eq $currentSid) {
        Write-Warning 'Password reset refused: the target is an Administrator or the current operator account. This recovery path is only for a separate non-administrator service account.'
        Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('select the dedicated non-administrator Agent_b service account')
        exit 1
    }
}

if (-not $existing -and $WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no password will be requested and no account or group will be changed.'
    $null = $PSCmdlet.ShouldProcess($qualifiedName, 'Create a non-administrator local account with a non-expiring password')
    Write-Host 'Would retain ordinary Users membership because removing it can prevent process launch.'
    Write-Host 'Would ensure the newly created account is not a member of Administrators.'
    Write-Host 'Would validate the supplied credential with LogonUser after creation.'
    Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('rerun this script alone without -WhatIf to enter and confirm the password')
    exit 0
}

$operation = if ($existing) { 'reset' } else { 'create' }
$securePassword = Read-VerifiedPassword -QualifiedName $qualifiedName
if (-not $securePassword) {
    Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights') -Next @('rerun this script alone and enter two matching, non-command-shaped password values')
    exit 2
}

try {
    if (Test-ConfirmationPromptExpected -Impact Medium) {
        Assert-SafeInteractiveInput -NonInteractiveParameter '-Confirm:$false'
    }
    $action = if ($operation -eq 'create') { 'Create a non-administrator local account with a non-expiring password' } else { 'Reset the existing local account password' }
    if (-not $PSCmdlet.ShouldProcess($qualifiedName, $action)) {
        $validationStatus = if ($WhatIfPreference) { 'credential validation was not run because WhatIf made no password change' } else { 'credential validation was not run because the operation was cancelled' }
        Write-SetupSummary -Changed @() -NotChanged @('account', 'password', 'group memberships', 'logon rights', $validationStatus) -Next @('rerun the command when ready to apply the requested change')
        exit 0
    }

    if ($operation -eq 'create') {
        $script:changeState = 'account creation was attempted; inspect the account because the operation failed before post-condition verification'
        New-LocalUser -Name $AccountName -Password $securePassword -PasswordNeverExpires -AccountNeverExpires -Description 'Dedicated low-privilege account for Agent_b' | Out-Null
        $created = Get-LocalUser -Name $AccountName
        $administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
        $adminMembership = Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue | Where-Object { $_.SID -eq $created.SID }
        if ($adminMembership) { Remove-LocalGroupMember -Group $administrators -Member $created -Confirm:$false }
        $script:changeState = 'local account was created and Administrators membership was checked'
    } else {
        $script:changeState = 'password reset was attempted; inspect the account because the operation failed before post-condition verification'
        Set-LocalUser -Name $AccountName -Password $securePassword
        $script:changeState = 'existing account password was reset'
    }

    $validation = Test-LocalCredential -Name $AccountName -Password $securePassword
    if (-not $validation.Valid) {
        [Console]::Error.WriteLine("VALIDATION FAILED: the account exists, but the script could not establish that the password took (Win32 $($validation.ErrorCode): $($validation.Message)).")
        $changed = if ($operation -eq 'create') { 'local account was created' } else { 'password reset was submitted' }
        $untouched = if ($operation -eq 'create') { 'ordinary Users membership' } else { 'account and group memberships' }
        Write-SetupSummary -Changed @($changed) -NotChanged @('logon rights', $untouched) -Next @('do not store this credential in Agent_b', 'resolve the validation failure and rerun with -ResetPassword')
        exit 1
    }

    Write-Host 'VALIDATED: the supplied credential successfully authenticated with LogonUser.'
    $changed = if ($operation -eq 'create') { 'created the non-administrator local account and set a verified password' } else { 'reset and verified the existing account password' }
    $untouched = if ($operation -eq 'create') { 'ordinary Users membership was retained' } else { 'account and group memberships' }
    Write-SetupSummary -Changed @($changed) -NotChanged @('interactive or task-scheduler logon rights', $untouched) -Next @('store the password in Agent_b Settings', 'enable and test the service identity', 'verify whoami before applying ACLs or firewall policy')
} finally {
    $securePassword.Dispose()
}
