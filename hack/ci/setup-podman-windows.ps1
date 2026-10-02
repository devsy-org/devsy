param(
    [Parameter(Mandatory = $true)][string]$PodmanPath,
    [switch]$FunctionsOnly
)

$ErrorActionPreference = 'Stop'
$initTimeout = [TimeSpan]::FromSeconds(90)
$startTimeout = [TimeSpan]::FromSeconds(90)
$readinessTimeout = [TimeSpan]::FromSeconds(45)
$probeTimeout = [TimeSpan]::FromSeconds(7)

function Invoke-BoundedCommand {
    param([string]$Path, [string[]]$Arguments, [TimeSpan]$Timeout)

    $stdout = [IO.Path]::GetTempFileName()
    $stderr = [IO.Path]::GetTempFileName()
    try {
        $process = Start-Process -FilePath $Path -ArgumentList $Arguments -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        if (-not $process.WaitForExit([int]$Timeout.TotalMilliseconds)) {
            # Podman can spawn forwarding processes. Kill the child tree before returning.
            try { $process.Kill($true) } catch { Write-Host "[podman-windows] kill child tree: $($_.Exception.Message)" }
            $null = $process.WaitForExit(5000)
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

function Write-MachineDiagnostics {
    Write-Host '[podman-windows] diagnostics start'
    foreach ($entry in @(
        @{ Path = $PodmanPath; Args = @('version') },
        @{ Path = $PodmanPath; Args = @('machine', 'list') },
        @{ Path = $PodmanPath; Args = @('machine', 'inspect') },
        @{ Path = $PodmanPath; Args = @('system', 'connection', 'list') },
        @{ Path = 'wsl.exe'; Args = @('--status') },
        @{ Path = 'wsl.exe'; Args = @('-l', '-v') }
    )) {
        $result = Invoke-BoundedCommand $entry.Path $entry.Args ([TimeSpan]::FromSeconds(3))
        Write-Host "[podman-windows] $($entry.Path) $($entry.Args -join ' '): exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
    }
    $processes = Invoke-BoundedCommand (Join-Path $PSHOME 'pwsh.exe') @(
        '-NoProfile', '-Command',
        "Get-Process | Where-Object { `$_.Name -match 'podman|wsl|gvproxy|win-sshproxy' } | Select-Object Id,ProcessName,StartTime"
    ) ([TimeSpan]::FromSeconds(3))
    Write-Host "[podman-windows] process table: exit=$($processes.ExitCode) timeout=$($processes.TimedOut) $($processes.Output)"
}

function Start-MachineAttempt {
    param([int]$Attempt)
    foreach ($entry in @(@{ Args = @('machine', 'init') }, @{ Args = @('machine', 'set', '--rootful') })) {
        $machineArgs = $entry.Args
        $timeout = if ($machineArgs[1] -eq 'init') { $initTimeout } else { [TimeSpan]::FromSeconds(20) }
        $started = Get-Date
        $result = Invoke-BoundedCommand $PodmanPath $machineArgs $timeout
        Write-Host "[podman-windows] $($machineArgs -join ' ') attempt=$Attempt elapsed=$((Get-Date) - $started)"
        if ($machineArgs[1] -eq 'init' -and $result.TimedOut) {
            return "PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT attempt=$Attempt output=$($result.Output)"
        }
        if ($machineArgs[1] -eq 'init' -and -not $result.TimedOut -and
            $result.ExitCode -ne 0 -and $result.Output -match 'already exists') {
            $existing = Invoke-BoundedCommand $PodmanPath @('machine', 'inspect') ([TimeSpan]::FromSeconds(5))
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
    $started = Get-Date
    $result = Invoke-BoundedCommand $PodmanPath @('machine', 'start') $startTimeout
    Write-Host "[podman-windows] machine start elapsed=$((Get-Date) - $started)"
    if ($result.TimedOut) { return "PODMAN_WINDOWS_MACHINE_START_TIMEOUT attempt=$Attempt" }
    if ($result.ExitCode -ne 0) { return "PODMAN_WINDOWS_MACHINE_START_FAILED attempt=$Attempt output=$($result.Output)" }

    $started = Get-Date
    do {
        $remaining = $readinessTimeout - ((Get-Date) - $started)
        if ($remaining.TotalMilliseconds -le 0) { break }
        $currentProbeTimeout = if ($remaining -lt $probeTimeout) { $remaining } else { $probeTimeout }
        $result = Invoke-BoundedCommand $PodmanPath @('info') $currentProbeTimeout
        if (-not $result.TimedOut -and $result.ExitCode -eq 0) {
            Write-Host "[podman-windows] readiness elapsed=$((Get-Date) - $started)"
            return $null
        }
        $remaining = $readinessTimeout - ((Get-Date) - $started)
        if ($remaining.TotalMilliseconds -gt 0) {
            Start-Sleep -Milliseconds ([int][Math]::Min(2000, $remaining.TotalMilliseconds))
        }
    } while (((Get-Date) - $started) -lt $readinessTimeout)
    return "PODMAN_WINDOWS_READINESS_TIMEOUT attempt=$Attempt last=$($result.Output)"
}

function Remove-StalePodmanDistribution {
    $distributions = Invoke-BoundedCommand 'wsl.exe' @('--list', '--quiet') ([TimeSpan]::FromSeconds(5))
    if ($distributions.TimedOut -or $distributions.ExitCode -ne 0) {
        Write-Host "[podman-windows] WSL distribution list unavailable: $($distributions.Output)"
        return
    }
    $names = ($distributions.Output -replace "`0", '') -split "`r?`n" | ForEach-Object { $_.Trim().Trim([char]0xFEFF) }
    if ($names -contains 'podman-machine-default') {
        $result = Invoke-BoundedCommand 'wsl.exe' @('--unregister', 'podman-machine-default') ([TimeSpan]::FromSeconds(15))
        Write-Host "[podman-windows] unregister exact Podman distribution: exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
    }
}

function Invoke-PodmanBootstrap {
    if (-not (Test-Path $PodmanPath)) { throw "podman.exe not found: $PodmanPath" }
    $result = Invoke-BoundedCommand 'wsl.exe' @('--set-default-version', '2') ([TimeSpan]::FromSeconds(15))
    if ($result.TimedOut -or $result.ExitCode -ne 0) { throw "WSL2 setup failed: $($result.Output)" }

    for ($attempt = 1; $attempt -le 2; $attempt++) {
        $failure = Start-MachineAttempt $attempt
        if (-not $failure) { Write-Host "[podman-windows] runtime ready attempt=$attempt"; return }
        Write-Host "[podman-windows] $failure"
        try { Write-MachineDiagnostics } catch { Write-Host "[podman-windows] diagnostics failed: $($_.Exception.Message)" }
        if ($attempt -eq 2) { throw "PODMAN_WINDOWS_RECOVERY_FAILED: $failure" }
        Write-Host '[podman-windows] recovery start'
        foreach ($entry in @(
            @{ Path = $PodmanPath; Args = @('machine', 'stop') },
            @{ Path = $PodmanPath; Args = @('machine', 'rm', '-f') },
            @{ Path = 'wsl.exe'; Args = @('--shutdown') }
        )) {
            $result = Invoke-BoundedCommand $entry.Path $entry.Args ([TimeSpan]::FromSeconds(10))
            Write-Host "[podman-windows] reset $($entry.Path) $($entry.Args -join ' '): exit=$($result.ExitCode) timeout=$($result.TimedOut) $($result.Output)"
        }
        Remove-StalePodmanDistribution
    }
}

if (-not $FunctionsOnly) { Invoke-PodmanBootstrap }
