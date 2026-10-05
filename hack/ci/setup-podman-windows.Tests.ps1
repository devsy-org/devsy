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
$realStopper = ${function:Stop-TimedOutProcess}
$completed = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Write-Output short-command-complete') ([TimeSpan]::FromSeconds(4))
Assert-True (-not $completed.TimedOut -and $completed.ExitCode -eq 0 -and $completed.Output -match 'short-command-complete') 'short command did not complete inside its timeout'

$started = Get-Date
$timedOut = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Start-Sleep -Seconds 30') ([TimeSpan]::FromMilliseconds(500))
Assert-True ($timedOut.TimedOut -and ((Get-Date) - $started).TotalSeconds -lt 5) 'process termination exceeded the command budget'
Assert-True ($null -eq (Get-Process -Id $timedOut.ProcessId -ErrorAction SilentlyContinue)) 'timed-out process remained alive'

$descendantTemp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $descendantTemp
$parentScript = Join-Path $descendantTemp 'parent.ps1'
$childPidFile = Join-Path $descendantTemp 'child.pid'
@'
$pwshPath = if ($IsWindows) { Join-Path $PSHOME 'pwsh.exe' } else { Join-Path $PSHOME 'pwsh' }
$child = Start-Process -FilePath $pwshPath -ArgumentList @('-NoProfile', '-Command', 'Start-Sleep -Seconds 30') -PassThru
$child.Id | Set-Content -Path "$($args[0]).tmp"
Move-Item -Path "$($args[0]).tmp" -Destination $args[0]
Start-Sleep -Seconds 30
'@ | Set-Content -Path $parentScript
$parentProcess = $null
$childPid = $null
try {
    $parentProcess = Start-Process -FilePath $pwshPath -ArgumentList @('-NoProfile', '-File', "`"$parentScript`"", "`"$childPidFile`"") -PassThru
    $deadline = (Get-Date).AddSeconds(15)
    while (-not (Test-Path $childPidFile) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 50 }
    Assert-True (Test-Path $childPidFile) 'descendant readiness timed out before termination test'
    $childPid = [int](Get-Content $childPidFile -Raw)
    Assert-True ($null -ne (Get-Process -Id $childPid -ErrorAction SilentlyContinue)) 'ready descendant exited before termination test'
    Assert-True (-not $parentProcess.WaitForExit(500)) 'parent exited before timeout handler was exercised'
    $started = Get-Date
    $stopped = & $realStopper $parentProcess ([TimeSpan]::FromSeconds(1))
    Assert-True ($stopped -and ((Get-Date) - $started).TotalSeconds -lt 3) 'direct parent termination exceeded its bounded grace'
    Assert-True ($null -eq (Get-Process -Id $parentProcess.Id -ErrorAction SilentlyContinue)) 'directly stopped parent process remained alive'
    Assert-True ($null -ne (Get-Process -Id $childPid -ErrorAction SilentlyContinue)) 'direct process stopper synchronously killed its descendant'
} finally {
    if ($parentProcess -and -not $parentProcess.HasExited) { Stop-Process -Id $parentProcess.Id -Force -ErrorAction SilentlyContinue }
    if (-not $childPid -and (Test-Path $childPidFile)) { $childPid = [int](Get-Content $childPidFile -Raw) }
    if ($childPid -and (Get-Process -Id $childPid -ErrorAction SilentlyContinue)) {
        Stop-Process -Id $childPid -Force -ErrorAction SilentlyContinue
    }
    Remove-Item $descendantTemp -Recurse -Force -ErrorAction SilentlyContinue
}

function Stop-TimedOutProcess {
    param([Diagnostics.Process]$Process, [TimeSpan]$Grace)
    throw 'simulated termination failure'
}
$terminationFailure = & $realRunner $pwshPath @('-NoProfile', '-Command', 'Start-Sleep -Seconds 30') ([TimeSpan]::FromMilliseconds(500))
Assert-True ($terminationFailure.TimedOut -and $terminationFailure.Output -match 'direct_process_stopped=False') 'termination failure did not return a timeout result'
if (Get-Process -Id $terminationFailure.ProcessId -ErrorAction SilentlyContinue) {
    Stop-Process -Id $terminationFailure.ProcessId -Force -ErrorAction SilentlyContinue
}
Set-Item Function:Stop-TimedOutProcess $realStopper

function Invoke-BoundedCommand {
    param([string]$Path, [string[]]$Arguments, [TimeSpan]$Timeout)

    $command = $Arguments -join ' '
    $script:calls.Add($command)
    $script:timeouts.Add($Timeout.TotalMilliseconds)
    if ($script:durationQueues.ContainsKey($command) -and $script:durationQueues[$command].Count -gt 0) {
        $duration = $script:durationQueues[$command].Dequeue()
        $script:elapsedMilliseconds += [Math]::Min($duration, $Timeout.TotalMilliseconds)
        if ($duration -gt $Timeout.TotalMilliseconds) { return @{ TimedOut = $true; ExitCode = $null; Output = 'timed out' } }
    } elseif ($script:durations.ContainsKey($command)) {
        $duration = $script:durations[$command]
        $script:elapsedMilliseconds += [Math]::Min($duration, $Timeout.TotalMilliseconds)
        if ($duration -gt $Timeout.TotalMilliseconds) {
            return @{ TimedOut = $true; ExitCode = $null; Output = 'timed out' }
        }
    }
    if ($script:overruns.ContainsKey($command)) {
        $script:elapsedMilliseconds = $script:overruns[$command]
        return @{ TimedOut = $false; ExitCode = 0; Output = 'completed after deadline' }
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
    $script:durationQueues = @{}
    $script:overruns = @{}
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
$script:overruns['--set-default-version 2'] = 1000
$budgetExpired = Invoke-BudgetedCommand (New-TestBudget 500) 'wsl.exe' @('--set-default-version', '2') ([TimeSpan]::FromSeconds(5))
Assert-True ($budgetExpired.TimedOut -and $null -eq $budgetExpired.ExitCode) 'command success after the shared deadline was accepted'
Assert-True ($budgetExpired.Output -match 'PODMAN_WINDOWS_BOOTSTRAP_BUDGET_EXHAUSTED') 'post-command budget exhaustion marker missing'

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
Assert-True ($script:elapsedMilliseconds -le 270000) 'recovery scenario exceeded the shared bootstrap budget'

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
$script:elapsedMilliseconds = 100001
$script:durations['machine init'] = 100000
$script:durations['version'] = 1000
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected insufficient init budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED: PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT') 'insufficient init marker missing'
}
Assert-Equal (($calls | Where-Object { $_ -eq 'machine init' }).Count) 1 'init timeout was not capped to available first-attempt budget'
Assert-Equal $timeouts[$calls.IndexOf('machine init')] 84999 'first init did not reserve diagnostics and recovery time'
Assert-True (($calls | Where-Object { $_ -eq 'version' }).Count -ge 1) 'terminal diagnostics did not run'
Assert-True (($calls | Where-Object { $_ -eq 'machine stop' }).Count -eq 0) 'recovery ran without enough time'

Set-Scenario
try {
    Invoke-PodmanBootstrap (New-TestBudget 84999)
    throw 'expected insufficient first init budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED: PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT') 'no-init budget marker missing'
}
Assert-Equal (($calls | Where-Object { $_ -eq 'machine init' }).Count) 0 'init launched without any time after required reserves'

Set-Scenario
Add-Result 'machine start' $failed
$script:durations['machine init'] = 150000
$script:durations['machine set --rootful'] = 20000
$script:durations['machine start'] = 45000
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected insufficient retry budget failure'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED:') 'insufficient retry marker missing'
}
Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 1 'slow init was not allowed to complete'
Assert-Equal ($timeouts[1]) 150000 'first init cap changed'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine start' }).Count 1 'insufficient-budget retry'
Assert-True (($calls | Where-Object { $_ -eq 'version' }).Count -eq 1) 'insufficient-budget diagnostics missing'
$versionIndex = $calls.IndexOf('version')
Assert-True ($timeouts[$versionIndex] -le 3000) 'terminal diagnostics exceeded the remaining diagnostic budget'

Set-Scenario
Add-Result 'machine start' $failed
$script:durations['machine init'] = 70000
$script:durations['machine set --rootful'] = 20000
$script:durations['machine start'] = 50000
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
$script:durations['machine init'] = 110000
$script:durations['machine set --rootful'] = 20000
$script:durations['machine start'] = 20000
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 1 'slow successful init count'
Assert-True ($timeouts[1] -ge 110000) 'slow successful init was not allowed to exceed 90 seconds'

Assert-True (-not (Test-MachineAbsentResult @{ TimedOut = $true; ExitCode = $null; Output = 'podman-machine-default: VM does not exist' })) 'timed-out machine removal was treated as confirmed absence'
Assert-True (-not (Test-MachineAbsentResult @{ TimedOut = $false; ExitCode = 1; Output = 'another-machine: VM does not exist' })) 'unrelated machine absence was accepted'
Assert-True (-not (Test-MachineAbsentResult @{ TimedOut = $false; ExitCode = 1; Output = "podman-machine-default: VM does not exist`npipe busy" })) 'mixed cleanup failure was swallowed as machine absence'
Assert-True (Test-MachineAbsentResult @{ TimedOut = $false; ExitCode = 125; Output = 'Error: podman-machine-default: VM does not exist' }) 'exact machine absence was not recognized'

Set-Scenario
$script:durationQueues['machine init'] = [Collections.Generic.Queue[double]]::new()
$script:durationQueues['machine init'].Enqueue(151000)
$script:durationQueues['machine init'].Enqueue(20000)
$absent = @{ TimedOut = $false; ExitCode = 125; Output = 'Error: podman-machine-default: VM does not exist' }
Add-Result 'machine inspect' $absent
Add-Result 'machine stop' $absent
Add-Result 'machine rm -f' $absent
$script:distributionList = "Ubuntu`npodman-machine-default`npodman-machine-default-other"
Invoke-PodmanBootstrap (New-TestBudget)
Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 2 'retry did not perform a fresh init after timed-out init cleanup'
Assert-Equal ($calls | Where-Object { $_ -eq '--unregister podman-machine-default' }).Count 1 'timed-out init recovery did not unregister the exact stale distribution'
Assert-Equal $timeouts[$calls.IndexOf('machine init', 2)] 95000 'second init did not receive its fresh remaining budget'
Assert-Equal ($calls | Where-Object { $_ -like '--unregister*' }).Count 1 'partial-init recovery unregistered an unrelated distribution'
$cleanupIndex = $calls.IndexOf('machine stop')
Assert-Equal (($calls.GetRange($cleanupIndex, 9)) -join ',') 'machine stop,machine rm -f,--shutdown,--list --quiet,--unregister podman-machine-default,machine init,machine set --rootful,machine start,info' 'partial-init recovery sequence'
Assert-True ($script:elapsedMilliseconds -le 270000) 'partial-init recovery exceeded the shared bootstrap budget'

foreach ($cleanupCommand in @('machine stop', 'machine rm -f', '--shutdown', '--list --quiet', '--unregister podman-machine-default')) {
    Set-Scenario
    Add-Result 'machine init' @{ TimedOut = $true; ExitCode = $null; Output = 'timed out' }
    Add-Result $cleanupCommand $failed
    $script:distributionList = 'podman-machine-default'
    try {
        Invoke-PodmanBootstrap (New-TestBudget)
        throw "expected unexpected cleanup failure for $cleanupCommand"
    } catch {
        Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED: recovery cleanup command failed') "unexpected cleanup failure was accepted for $cleanupCommand"
    }
    Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 1 "retry began after failed $cleanupCommand"
}

Set-Scenario
Add-Result 'machine start' $failed
$script:durations['machine init'] = 150000
$script:durations['machine set --rootful'] = 20000
$script:durations['machine start'] = 5000
$script:durations['machine stop'] = 10000
$script:durations['machine rm -f'] = 10000
$script:durations['--shutdown'] = 10000
$script:durations['--list --quiet'] = 10000
$script:distributionList = "Ubuntu`npodman-machine-default"
try {
    Invoke-PodmanBootstrap (New-TestBudget)
    throw 'expected cleanup budget exhaustion'
} catch {
    Assert-True ($_.Exception.Message -match '^PODMAN_WINDOWS_RECOVERY_FAILED:') 'cleanup budget failure marker missing'
}
Assert-Equal ($calls | Where-Object { $_ -eq 'machine stop' }).Count 1 'cleanup-budget scenario did not enter recovery'
Assert-Equal ($calls | Where-Object { $_ -eq 'machine init' }).Count 1 'second init began after cleanup consumed the shared budget'
Assert-True ($script:elapsedMilliseconds -le 270000) 'cleanup-budget scenario exceeded the shared bootstrap budget'

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
