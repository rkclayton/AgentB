[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [ValidateSet('Install', 'Status', 'Uninstall')]
    [string]$Action = 'Status'
)

$ErrorActionPreference = 'Stop'
$taskName = 'Agent_b Plan Validation Watcher'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$supervisorPath = Join-Path $PSScriptRoot 'plan-validation-supervisor.mjs'
$node = (Get-Command node.exe -ErrorAction Stop).Source

switch ($Action) {
    'Install' {
        if (-not $PSCmdlet.ShouldProcess($taskName, 'register and start current-user validation watcher task')) { return }
        $taskAction = New-ScheduledTaskAction -Execute $node -Argument ('"' + $supervisorPath + '"') -WorkingDirectory $repositoryRoot
        $trigger = New-ScheduledTaskTrigger -AtLogOn -User ([Security.Principal.WindowsIdentity]::GetCurrent().Name)
        $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
        $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
        $task = New-ScheduledTask -Action $taskAction -Trigger $trigger -Principal $principal -Settings $settings
        Register-ScheduledTask -TaskName $taskName -InputObject $task -Force | Out-Null
        Start-ScheduledTask -TaskName $taskName
        Get-ScheduledTask -TaskName $taskName | Select-Object TaskName, State
    }
    'Status' {
        Get-ScheduledTask -TaskName $taskName | Select-Object TaskName, State
    }
    'Uninstall' {
        if ($PSCmdlet.ShouldProcess($taskName, 'stop and unregister validation watcher task')) {
            Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
            Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
        }
    }
}
