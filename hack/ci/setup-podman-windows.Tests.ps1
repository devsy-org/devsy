$ErrorActionPreference = 'Stop'
$pwshPath = if ($IsWindows) { Join-Path $PSHOME 'pwsh.exe' } else { Join-Path $PSHOME 'pwsh' }
. "$PSScriptRoot/setup-podman-windows.ps1" -PodmanPath $pwshPath -FunctionsOnly

function Assert-Equal($actual, $expected, $label) {
    if ($actual -ne $expected) { throw "$label expected=$expected actual=$actual" }
}

function Assert-True($condition, $label) {
    if (-not $condition) { throw $label }
}

$realRunner = ${function:Invoke-BoundedCommand}
$completed = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Write-Output short-command-complete') ([TimeSpan]::FromSeconds(4))
Assert-True (-not $completed.TimedOut -and $completed.ExitCode -eq 0 -and $completed.Output -match 'short-command-complete') 'short command did not complete inside its timeout'

$started = Get-Date
$timedOut = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Start-Sleep -Seconds 30') ([TimeSpan]::FromMilliseconds(500))
Assert-True ($timedOut.TimedOut -and ((Get-Date) - $started).TotalSeconds -lt 1) 'process termination exceeded the command budget'

function Invoke-BoundedCommand {
    param([string]$Path, [string[]]$Arguments, [TimeSpan]$Timeout)

    $command = $Arguments -join ' '
    $script:calls.Add($command)
    $script:timeouts.Add($Timeout.TotalMilliseconds)
    if ($script:durations.ContainsKey($command)) {
        $duration = $script:durations[$command]
        $script:elapsedMilliseconds += [Math]::Min($duration, $Timeout.TotalMilliseconds)
        if ($duration -gt $Timeout.TotalMilliseconds) {
            return @{ TimedOut = $true; ExitCode = $null; Output = 'timed out' }
        }
    }
    if ($script:results.ContainsKey($command) -and $script:results[$command].Count -gt 0) {
        return $script:results[$command].Dequeue()
    }
    if ($command -eq '--list --quiet') {
        return @{ TimedOut = $false; ExitCode = 0; Output = $script:distributionList }
    }
    return @{ TimedOut = $false; ExitCode = 0; Output = '' }
}

function Start-Sleep {
    param([int]$Milliseconds)
    $script:elapsedMilliseconds += $Milliseconds
}

function New-TestBudget([double]$milliseconds = 270000) {
    return New-BootstrapBudget ([TimeSpan]::FromMilliseconds($milliseconds)) { [TimeSpan]::FromMilliseconds($script:elapsedMilliseconds) }
}

function Set-Scenario {
    $script:calls = [Collections.Generic.List[string]]::new()
    $script:timeouts = [Collections.Generic.List[double]]::new()
    $script:durations = @{}
    $script:results = @{}
    $script:elapsedMilliseconds = 0
    $script:distributionList = ''
}

function Add-Result([string]$command, [hashtable]$result) {
    if (-not $script:results.ContainsKey($command)) {
        $script:results[$command] = [Collections.Generic.Queue[hashtable]]::new()
    }
    $script:results[$command].Enqueue($result)
}

$ok = @{ TimedOut = $false; ExitCode = 0; Output = '' }
$failed = @{ TimedOut = $false; ExitCode = 1; Output = 'pipe busy' }

Set-Scenario
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 1 'healthy starts'
Assert-Equal ($calls | Where-Object { $_ -eq 'version' }).Count 0 'healthy diagnostics'

Set-Scenario
Add-Result 'machine init' @{ TimedOut = $false; ExitCode = 125; Output = 'machine already exists' }
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -eq 'machine inspect' }).Count 1 'existing machine inspect'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine set --rootful' }).Count 1 'existing machine rootful setup'

Set-Scenario
Add-Result 'machine start' $failed
Add-Result 'machine start' $ok
$script:distributionList = "Ubuntu`npodman-machine-default"
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 2 'recovery starts'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine rm -f' }).Count 1 'recovery reset'
Assert-Equal ($calls | Where-Object { $_ -eq '--unregister podman-machine-default' }).Count 1 'exact stale distribution cleanup'

Set-Scenario
Add-Result 'machine start' $failed
Add-Result 'machine start' $ok
$script:distributionList = 'Ubuntu'
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -like '--unregister*' }).Count 0 'unrelated distribution preserved'

Set-Scenario
$script:durations['--set-default-version 2'] = 11000
try {
    Invoke-PodmanBootstrap (New-TestBudget 35000)
    throw 'expected exhausted budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED: WSL2 setup failed') 'exhausted budget marker missing'
}
Assert-Equal $timeouts[0] 10000 'command did not preserve terminal diagnostic budget'
Assert-True (($calls | Where-Object { $_ -eq 'version' }).Count -eq 1) 'exhausted-budget diagnostics missing'

Set-Scenario
$script:durations['machine init'] = 100000
try {
    Invoke-PodmanBootstrap (New-TestBudget 75000)
    throw 'expected near-deadline failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED: PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT') 'near-deadline marker missing'
}
Assert-Equal $timeouts[1] 50000 'near-deadline command was not capped by remaining budget'
Assert-True (($calls | Where-Object { $_ -eq 'version' }).Count -eq 1) 'terminal diagnostics did not run'
Assert-True (($calls | Where-Object { $_ -eq 'machine stop' }).Count -eq 0) 'recovery ran without enough time'

Set-Scenario
Add-Result 'machine start' $failed
$script:elapsedMilliseconds = 155000
$script:durations['machine start'] = 100000
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected insufficient retry budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED:') 'insufficient retry marker missing'
}
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 1 'insufficient-budget retry'
Assert-True (($calls | Where-Object { $_ -eq 'version' }).Count -eq 1) 'insufficient-budget diagnostics missing'
$versionIndex = $calls.IndexOf('version')
Assert-Equal $timeouts[$versionIndex] 3000 'terminal diagnostics did not use the reserved budget'

Set-Scenario
Add-Result 'machine start' $failed
$script:elapsedMilliseconds = 170000
$script:durations['machine stop'] = 20000
$script:durations['machine rm -f'] = 20000
$script:durations['--shutdown'] = 20000
$script:durations['--list --quiet'] = 20000
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected cleanup budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED:') 'post-cleanup marker missing'
}
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 1 'second attempt began after cleanup consumed its budget'
Assert-Equal ($calls | Where-Object { $_ -eq 'version' }).Count 2 'post-cleanup terminal diagnostics missing'

Set-Scenario
Add-Result 'machine start' $failed
Add-Result 'machine start' $failed
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected recovery failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED:.*pipe busy') 'terminal recovery marker missing'
}
Assert-Equal ($calls | Where-Object { $_ -eq 'version' }).Count 2 'terminal diagnostics count'

Write-Host '[podman-windows] bootstrap helper tests passed'
