$ErrorActionPreference = 'Stop'
$pwshPath = Join-Path $PSHOME 'pwsh.exe'
. "$PSScriptRoot/setup-podman-windows.ps1" -PodmanPath $pwshPath -FunctionsOnly

function Assert-Equal($actual, $expected, $label) {
    if ($actual -ne $expected) { throw "$label expected=$expected actual=$actual" }
}

$realRunner = ${function:Invoke-BoundedCommand}
$started = Get-Date
$timedOut = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Start-Sleep -Seconds 30') ([TimeSpan]::FromMilliseconds(200))
if (-not $timedOut.TimedOut -or ((Get-Date) - $started).TotalSeconds -gt 8) {
    throw 'hung child process did not stop within the test bound'
}

function Invoke-BoundedCommand {
    param([string]$Path, [string[]]$Arguments, [TimeSpan]$Timeout)
    $command = $Arguments -join ' '
    $script:calls.Add($command)
    if ($command -eq 'machine init') {
        $script:initTimeouts.Add($Timeout.TotalSeconds)
        if ($script:initResults.Count -gt 0) { return $script:initResults.Dequeue() }
    }
    if ($command -eq 'machine inspect' -and $script:inspectResults.Count -gt 0) { return $script:inspectResults.Dequeue() }
    if ($command -eq '--list --quiet') {
        return @{ TimedOut = $false; ExitCode = 0; Output = $script:distributionList }
    }
    if ($command -eq 'machine start') { return $script:startResults.Dequeue() }
    if ($command -eq 'info' -and $script:readinessFailsFirstAttempt -and
        ($script:calls | Where-Object { $_ -eq 'machine start' }).Count -eq 1) {
        return @{ TimedOut = $false; ExitCode = 1; Output = 'not ready' }
    }
    if ($command -eq 'info' -and $script:infoResults.Count -gt 0) { return $script:infoResults.Dequeue() }
    return @{ TimedOut = $false; ExitCode = 0; Output = '' }
}

function Write-MachineDiagnostics {
    $script:diagnostics++
    if ($script:diagnosticsFail) { throw 'diagnostic tool unavailable' }
}

function Set-Scenario($starts, $infos, $diagnosticsFail = $false) {
    $script:calls = [Collections.Generic.List[string]]::new()
    $script:startResults = [Collections.Generic.Queue[hashtable]]::new()
    $script:infoResults = [Collections.Generic.Queue[hashtable]]::new()
    $script:initResults = [Collections.Generic.Queue[hashtable]]::new()
    $script:inspectResults = [Collections.Generic.Queue[hashtable]]::new()
    $script:initTimeouts = [Collections.Generic.List[double]]::new()
    foreach ($result in $starts) { $script:startResults.Enqueue($result) }
    foreach ($result in $infos) { $script:infoResults.Enqueue($result) }
    $script:diagnostics = 0
    $script:diagnosticsFail = $diagnosticsFail
    $script:readinessFailsFirstAttempt = $false
    $script:distributionList = ''
}

$ok = @{ TimedOut = $false; ExitCode = 0; Output = '' }
$failed = @{ TimedOut = $false; ExitCode = 1; Output = 'pipe busy' }
$hung = @{ TimedOut = $true; ExitCode = $null; Output = 'timed out' }

Set-Scenario -starts @($ok) -infos @($ok)
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 1 'healthy starts'
Assert-Equal $diagnostics 0 'healthy diagnostics'
Assert-Equal $initTimeouts[0] 90 'machine init budget'

Set-Scenario -starts @($ok) -infos @($ok)
$script:initResults.Enqueue(@{ TimedOut = $false; ExitCode = 125; Output = 'machine already exists' })
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine set --rootful' }).Count 1 'existing machine rootful setup'

Set-Scenario -starts @($ok) -infos @($ok)
$script:initResults.Enqueue(@{ TimedOut = $false; ExitCode = 125; Output = 'machine already exists' })
$script:inspectResults.Enqueue(@{ TimedOut = $false; ExitCode = 125; Output = 'VM does not exist' })
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine set --rootful' }).Count 1 'partial machine record reset before rootful setup'

Set-Scenario -starts @($ok) -infos @($ok)
$script:initResults.Enqueue($hung)
$script:distributionList = "Ubuntu`n" + ('podman-machine-default'.ToCharArray() -join "`0")
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq '--unregister podman-machine-default' }).Count 1 'stale Podman distribution removed'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 2 'init retried after timeout'

Set-Scenario -starts @() -infos @()
$script:initResults.Enqueue($hung)
if ((Start-MachineAttempt 1) -notmatch '^PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT') {
    throw 'machine init timeout was not classified'
}

Set-Scenario -starts @($failed, $ok) -infos @($ok)
$script:distributionList = 'Ubuntu'
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -like '--unregister*' }).Count 0 'unrelated WSL distribution preserved'

Set-Scenario -starts @($failed, $ok) -infos @($ok)
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 2 'recovered starts'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine rm -f' }).Count 1 'resets'
Assert-Equal $diagnostics 1 'first failure diagnostics'

Set-Scenario -starts @($hung, $ok) -infos @($ok)
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 2 'hung start retry'

Set-Scenario -starts @($failed, $failed) -infos @() -diagnosticsFail $true
try {
    Invoke-PodmanBootstrap
    throw 'expected bootstrap failure'
} catch {
    if ($_.Exception.Message -notmatch 'PODMAN_WINDOWS_RECOVERY_FAILED.*pipe busy') { throw }
}
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 2 'terminal starts'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine rm -f' }).Count 1 'terminal resets'
Assert-Equal $diagnostics 2 'terminal diagnostics'

$readinessTimeout = [TimeSpan]::FromMilliseconds(200)
Set-Scenario -starts @($ok, $ok) -infos @($ok)
$script:readinessFailsFirstAttempt = $true
Invoke-PodmanBootstrap
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 2 'readiness retry'

Write-Host '[podman-windows] bootstrap helper tests passed'
