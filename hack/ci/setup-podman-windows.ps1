param(
    [Parameter(Mandatory = $true)][string]$PodmanPath,
    [TimeSpan]$BootstrapTimeout = ([TimeSpan]::FromSeconds(270)),
    [switch]$FunctionsOnly
)

$ErrorActionPreference = 'Stop'
$initTimeout = [TimeSpan]::FromSeconds(90)
$startTimeout = [TimeSpan]::FromSeconds(90)
$readinessTimeout = [TimeSpan]::FromSeconds(45)
$probeTimeout = [TimeSpan]::FromSeconds(7)
$diagnosticReserve = [TimeSpan]::FromSeconds(25)
$minimumRecoveryTime = [TimeSpan]::FromSeconds(60)
$maximumTerminationReserve = [TimeSpan]::FromSeconds(5)

function New-BootstrapBudget {
    param([TimeSpan]$Timeout, [scriptblock]$Elapsed = $null)

    if ($Timeout.TotalMilliseconds -le 0) { throw 'Bootstrap timeout must be positive' }
    if (-not $Elapsed) {
        $stopwatch = [Diagnostics.Stopwatch]::StartNew()
        $Elapsed = { $stopwatch.Elapsed }.GetNewClosure()
    }
    return @{ Timeout = $Timeout; Elapsed = $Elapsed }
}

function Get-BudgetRemaining {
    param([hashtable]$Budget, [TimeSpan]$Reserve = [TimeSpan]::Zero)

    $remaining = $Budget.Timeout - (& $Budget.Elapsed) - $Reserve
    if ($remaining.TotalMilliseconds -le 0) { return [TimeSpan]::Zero }
    return $remaining
}

function Invoke-BoundedCommand {
    param([string]$Path, [string[]]$Arguments, [TimeSpan]$Timeout)

    if ($Timeout.TotalMilliseconds -le 0) {
        return @{ TimedOut = $true; ExitCode = $null; Output = 'shared bootstrap budget exhausted' }
    }
    $stdout = [IO.Path]::GetTempFileName()
    $stderr = [IO.Path]::GetTempFileName()
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    try {
        $process = Start-Process -FilePath $Path -ArgumentList $Arguments -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        $terminationWait = [Math]::Min($maximumTerminationReserve.TotalMilliseconds, $Timeout.TotalMilliseconds * 0.2)
        $commandWait = [Math]::Max(0, $Timeout.TotalMilliseconds - $terminationWait - $stopwatch.Elapsed.TotalMilliseconds)
        if (-not $process.WaitForExit([int]$commandWait)) {
            try { $process.Kill($true) } catch { Write-Host "[podman-windows] kill child tree: $($_.Exception.Message)" }
            $remaining = [Math]::Max(0, $Timeout.TotalMilliseconds - $stopwatch.Elapsed.TotalMilliseconds)
            if ($remaining -gt 0) { $null = $process.WaitForExit([int]$remaining) }
            $output = (Get-Content $stdout -Raw -ErrorAction SilentlyContinue) + (Get-Content $stderr -Raw -ErrorAction SilentlyContinue)
            return @{ TimedOut = $true; ExitCode = $null; Output = "command timed out: $output" }
        }
        return @{ TimedOut = $false; ExitCode = $process.ExitCode; Output = ((Get-Content $stdout -Raw -ErrorAction SilentlyContinue) + (Get-Content $stderr -Raw -ErrorAction SilentlyContinue)) }
    } catch {
        return @{ TimedOut = $false; ExitCode = -1; Output = $_.Exception.Message }
    } finally {
        Remove-Item $stdout, $stderr -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-BudgetedCommand {
    param(
        [hashtable]$Budget,
        [string]$Path,
        [string[]]$Arguments,
        [TimeSpan]$Timeout,
        [TimeSpan]$Reserve = [TimeSpan]::Zero
    )

    $remaining = Get-BudgetRemaining $Budget $Reserve
    $boundedTimeout = if ($remaining -lt $Timeout) { $remaining } else { $Timeout }
    return Invoke-BoundedCommand $Path $Arguments $boundedTimeout
}

function Write-MachineDiagnostics {
    param([hashtable]$Budget, [TimeSpan]$Reserve = [TimeSpan]::Zero)

    Write-Host '[podman-windows] diagnostics start'
    foreach ($entry in @(
        @{ Path = $PodmanPath; Args = @('version') },
        @{ Path = $PodmanPath; Args = @('machine', 'list') },
        @{ Path = $PodmanPath; Args = @('machine', 'inspect') },
        @{ Path = $PodmanPath; Args = @('system', 'connection', 'list') },
        @{ Path = 'wsl.exe'; Args = @('--status') },
        @{ Path = 'wsl.exe'; Args = @('-l', '-v') }
    )) {
        $result = Invoke-BudgetedCommand $Budget $entry.Path $entry.Args ([TimeSpan]::FromSeconds(3)) $Reserve
        Write-Host "[podman-windows] $($entry.Path) $($entry.Args -join ' '): exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
    }
    $processes = Invoke-BudgetedCommand $Budget (Join-Path $PSHOME 'pwsh.exe') @(
        '-NoProfile', '-Command',
        "Get-Process | Where-Object { `$_.Name -match 'podman|wsl|gvproxy|win-sshproxy' } | Select-Object Id,ProcessName,StartTime"
    ) ([TimeSpan]::FromSeconds(3)) $Reserve
    Write-Host "[podman-windows] process table: exit=$($processes.ExitCode) timeout=$($processes.TimedOut) $($processes.Output)"
}

function Start-MachineAttempt {
    param([int]$Attempt, [hashtable]$Budget)

    foreach ($entry in @(
        @{ Args = @('machine', 'init'); Timeout = $initTimeout },
        @{ Args = @('machine', 'set', '--rootful'); Timeout = [TimeSpan]::FromSeconds(20) }
    )) {
        $machineArgs = $entry.Args
        $result = Invoke-BudgetedCommand $Budget $PodmanPath $machineArgs $entry.Timeout $diagnosticReserve
        if ($machineArgs[1] -eq 'init' -and $result.TimedOut) {
            return "PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT attempt=$Attempt output=$($result.Output)"
        }
        if ($machineArgs[1] -eq 'init' -and $result.ExitCode -ne 0 -and $result.Output -match 'already exists') {
            $existing = Invoke-BudgetedCommand $Budget $PodmanPath @('machine', 'inspect') ([TimeSpan]::FromSeconds(5)) $diagnosticReserve
            if ($existing.TimedOut -or $existing.ExitCode -ne 0) {
                return "PODMAN_WINDOWS_MACHINE_START_FAILED attempt=$Attempt machine exists without a usable record: $($existing.Output)"
            }
            Write-Host '[podman-windows] machine already exists; continuing to rootful setup'
            continue
        }
        if ($result.TimedOut -or $result.ExitCode -ne 0) {
            return "PODMAN_WINDOWS_MACHINE_START_FAILED attempt=$Attempt command=$($machineArgs -join ' ') output=$($result.Output)"
        }
    }

    Write-Host "[podman-windows] machine start attempt=$Attempt"
    $result = Invoke-BudgetedCommand $Budget $PodmanPath @('machine', 'start') $startTimeout $diagnosticReserve
    if ($result.TimedOut) { return "PODMAN_WINDOWS_MACHINE_START_TIMEOUT attempt=$Attempt" }
    if ($result.ExitCode -ne 0) { return "PODMAN_WINDOWS_MACHINE_START_FAILED attempt=$Attempt output=$($result.Output)" }

    $readinessStarted = & $Budget.Elapsed
    do {
        $readinessRemaining = $readinessTimeout - ((& $Budget.Elapsed) - $readinessStarted)
        $budgetRemaining = Get-BudgetRemaining $Budget $diagnosticReserve
        if ($readinessRemaining.TotalMilliseconds -le 0 -or $budgetRemaining.TotalMilliseconds -le 0) { break }
        $currentProbeTimeout = [TimeSpan]::FromMilliseconds([Math]::Min($probeTimeout.TotalMilliseconds, [Math]::Min($readinessRemaining.TotalMilliseconds, $budgetRemaining.TotalMilliseconds)))
        $result = Invoke-BudgetedCommand $Budget $PodmanPath @('info') $currentProbeTimeout $diagnosticReserve
        if (-not $result.TimedOut -and $result.ExitCode -eq 0) { return $null }
        $readinessRemaining = $readinessTimeout - ((& $Budget.Elapsed) - $readinessStarted)
        $budgetRemaining = Get-BudgetRemaining $Budget $diagnosticReserve
        $sleepMilliseconds = [Math]::Min(2000, [Math]::Min($readinessRemaining.TotalMilliseconds, $budgetRemaining.TotalMilliseconds))
        if ($sleepMilliseconds -gt 0) { Start-Sleep -Milliseconds ([int]$sleepMilliseconds) }
    } while ($true)
    return "PODMAN_WINDOWS_READINESS_TIMEOUT attempt=$Attempt last=$($result.Output)"
}

function Remove-StalePodmanDistribution {
    param([hashtable]$Budget)

    $distributions = Invoke-BudgetedCommand $Budget 'wsl.exe' @('--list', '--quiet') ([TimeSpan]::FromSeconds(5)) $diagnosticReserve
    if ($distributions.TimedOut -or $distributions.ExitCode -ne 0) {
        Write-Host "[podman-windows] WSL distribution list unavailable: $($distributions.Output)"
        return
    }
    $names = ($distributions.Output -replace "`0", '') -split "`r?`n" | ForEach-Object { $_.Trim().Trim([char]0xFEFF) }
    if ($names -contains 'podman-machine-default') {
        $result = Invoke-BudgetedCommand $Budget 'wsl.exe' @('--unregister', 'podman-machine-default') ([TimeSpan]::FromSeconds(15)) $diagnosticReserve
        Write-Host "[podman-windows] unregister exact Podman distribution: exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
    }
}

function Invoke-PodmanBootstrap {
    param([hashtable]$Budget = (New-BootstrapBudget $BootstrapTimeout))

    if (-not (Test-Path $PodmanPath)) { throw "podman.exe not found: $PodmanPath" }
    $result = Invoke-BudgetedCommand $Budget 'wsl.exe' @('--set-default-version', '2') ([TimeSpan]::FromSeconds(15)) $diagnosticReserve
    if ($result.TimedOut -or $result.ExitCode -ne 0) {
        try { Write-MachineDiagnostics $Budget } catch { Write-Host "[podman-windows] diagnostics failed: $($_.Exception.Message)" }
        throw "PODMAN_WINDOWS_RECOVERY_FAILED: WSL2 setup failed: $($result.Output)"
    }

    for ($attempt = 1; $attempt -le 2; $attempt++) {
        $failure = Start-MachineAttempt $attempt $Budget
        if (-not $failure) { Write-Host "[podman-windows] runtime ready attempt=$attempt"; return }
        Write-Host "[podman-windows] $failure"
        $canRecover = $attempt -eq 1 -and (Get-BudgetRemaining $Budget $diagnosticReserve) -ge $minimumRecoveryTime
        $diagnosticReservation = if ($canRecover) { $diagnosticReserve } else { [TimeSpan]::Zero }
        try { Write-MachineDiagnostics $Budget $diagnosticReservation } catch { Write-Host "[podman-windows] diagnostics failed: $($_.Exception.Message)" }
        if (-not $canRecover -or $attempt -eq 2) {
            throw "PODMAN_WINDOWS_RECOVERY_FAILED: $failure"
        }
        if ((Get-BudgetRemaining $Budget $diagnosticReserve) -lt $minimumRecoveryTime) {
            try { Write-MachineDiagnostics $Budget } catch { Write-Host "[podman-windows] diagnostics failed: $($_.Exception.Message)" }
            throw "PODMAN_WINDOWS_RECOVERY_FAILED: $failure"
        }

        Write-Host '[podman-windows] recovery start'
        foreach ($entry in @(
            @{ Path = $PodmanPath; Args = @('machine', 'stop') },
            @{ Path = $PodmanPath; Args = @('machine', 'rm', '-f') },
            @{ Path = 'wsl.exe'; Args = @('--shutdown') }
        )) {
            $result = Invoke-BudgetedCommand $Budget $entry.Path $entry.Args ([TimeSpan]::FromSeconds(10)) $diagnosticReserve
            Write-Host "[podman-windows] reset $($entry.Path) $($entry.Args -join ' '): exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
        }
        Remove-StalePodmanDistribution $Budget
        if ((Get-BudgetRemaining $Budget $diagnosticReserve) -lt $minimumRecoveryTime) {
            try { Write-MachineDiagnostics $Budget } catch { Write-Host "[podman-windows] diagnostics failed: $($_.Exception.Message)" }
            throw "PODMAN_WINDOWS_RECOVERY_FAILED: $failure"
        }
    }
}

if (-not $FunctionsOnly) { Invoke-PodmanBootstrap }
