$ErrorActionPreference = "Stop"

$root = (Get-Location).Path
$runnerTemp = $env:RUNNER_TEMP
$report = Join-Path $runnerTemp "t0-evidence.txt"
$sourceRevision = git -C $root rev-parse HEAD
$devsy = Join-Path $runnerTemp "devsy.exe"
$observerTest = Join-Path $runnerTemp "command-t0.test.exe"
$openTest = Join-Path $runnerTemp "vscode-t0.test.exe"
$fixtureRoot = Join-Path $runnerTemp "t0-fixtures"
$stateFile = Join-Path $runnerTemp "devsy-ci\docker-wsl-state.json"
$e2eHelper = Join-Path $runnerTemp "devsy-ci-e2e.exe"
$script:knownOutputSecrets = @(
    Get-ChildItem Env: |
        Where-Object { $_.Name -match '(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE_KEY|ACCESS_KEY)' } |
        ForEach-Object { [string]$_.Value } |
        Where-Object { $_.Length -ge 6 }
)
$script:knownOutputPaths = @()
$server = $null

New-Item -ItemType Directory -Force -Path (Split-Path $stateFile) | Out-Null
New-Item -ItemType Directory -Force -Path $fixtureRoot | Out-Null
Set-Content -Path $report -Value "sourceRevision=$sourceRevision" -Encoding ascii

function Add-Evidence([string]$line) {
    Add-Content -Path $report -Value $line -Encoding ascii
    Write-Host $line
}

function Quote-ProcessArgument([string]$value) {
    return '"' + $value.Replace('"', '\"') + '"'
}

function Start-QuietProcess([string]$path, [string[]]$arguments, [string]$stage) {
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    $stdout = Join-Path $runnerTemp "$safeStage.stdout"
    $stderr = Join-Path $runnerTemp "$safeStage.stderr"
    $argumentLine = ($arguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' '
    $process = Start-Process -FilePath $path -ArgumentList $argumentLine -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    return @{ Process = $process; Stdout = $stdout; Stderr = $stderr }
}

function Add-KnownDiagnosticSecret([string]$value) {
    if ($value -and $value.Length -ge 6 -and $script:knownOutputSecrets -notcontains $value) {
        $script:knownOutputSecrets += $value
    }
}

function Add-KnownDiagnosticPath([string]$path) {
    if (!$path) { return }
    foreach ($value in @($path, $path.Replace('\', '/'), $path.Replace('/', '\'))) {
        if ($value -and $script:knownOutputPaths -notcontains $value) {
            $script:knownOutputPaths += $value
        }
    }
}

function Redact-DiagnosticText([string]$text) {
    foreach ($secret in @($script:knownOutputSecrets | Sort-Object Length -Descending)) {
        $text = $text.Replace($secret, "[REDACTED]")
        $jsonSecret = ConvertTo-Json -InputObject $secret -Compress
        if ($jsonSecret.Length -gt 2) {
            $text = $text.Replace($jsonSecret.Substring(1, $jsonSecret.Length - 2), "[REDACTED]")
        }
    }
    foreach ($path in $script:knownOutputPaths) {
        $jsonPath = (ConvertTo-Json -InputObject $path -Compress).Trim('"')
        foreach ($value in @($path, $jsonPath)) {
            $text = [regex]::Replace($text, [regex]::Escape($value), "[REDACTED_PATH]", [System.Text.RegularExpressions.RegexOptions]::IgnoreCase)
        }
    }
    $redactions = @(
        @{ Pattern = '(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)'; Replacement = '-----BEGIN PRIVATE KEY----- [REDACTED] -----END PRIVATE KEY-----' },
        @{ Pattern = '\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b'; Replacement = '[REDACTED]' },
        @{ Pattern = '(?i)(authorization:\s*(?:bearer|basic|token)\s+)\S+'; Replacement = '${1}[REDACTED]' },
        @{ Pattern = '(?i)("(?:auth|identitytoken|registrytoken|password|token|access_token|refresh_token|client_secret|secret|credential|private_key)"\s*:\s*)"[^"]*"'; Replacement = '${1}"[REDACTED]"' },
        @{ Pattern = '(?i)(https?://[^/\s:@]+:)[^@\s/]+@'; Replacement = '${1}[REDACTED]@' },
        @{ Pattern = '(?i)\b([A-Za-z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE_KEY|ACCESS_KEY)[A-Za-z0-9_]*)(\s*[=:]\s*)(?!\[REDACTED\])(["'']?)([^"''\s,}\]]+)\3'; Replacement = '${1}${2}${3}[REDACTED]${3}' }
    )
    foreach ($redaction in $redactions) {
        $text = [regex]::Replace($text, $redaction.Pattern, $redaction.Replacement)
    }
    return [regex]::Replace($text, '[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]', '?')
}

function Read-BoundedProcessOutput([string]$path) {
    $file = Get-Item -LiteralPath $path
    $maxRedactionLength = 0
    foreach ($value in @($script:knownOutputSecrets) + @($script:knownOutputPaths)) {
        $maxRedactionLength = [Math]::Max($maxRedactionLength, $value.Length)
    }
    if ($maxRedactionLength -gt 65536) {
        return [pscustomobject]@{
            Bytes = $file.Length
            Text = "[diagnostic omitted: redaction context exceeds limit]"
            Truncated = $true
        }
    }
    $readLimit = 4096 + [Math]::Max(512, $maxRedactionLength)
    $reader = [System.IO.StreamReader]::new($path)
    $buffer = [char[]]::new($readLimit)
    try {
        $sampleLength = $reader.ReadBlock($buffer, 0, $buffer.Length)
        $truncated = $reader.Peek() -ge 0
    } finally {
        $reader.Dispose()
    }
    return [pscustomobject]@{
        Bytes = $file.Length
        Text = [string]::new($buffer, 0, $sampleLength)
        Truncated = $truncated
    }
}

function Select-SafeErrorFields($value, [int]$depth = 0) {
    if ($depth -gt 8) { return "[cause-depth-limit]" }
    if ($null -eq $value -or $value -is [ValueType]) { return $value }
    if ($value -is [string]) { return Redact-DiagnosticText $value }
    if ($value -is [System.Collections.IDictionary]) {
        $selected = [ordered]@{}
        foreach ($name in @("code", "kind", "message", "cause", "causes", "error")) {
            foreach ($key in $value.Keys) {
                if ([string]$key -ieq $name) {
                    $selected[$key] = Select-SafeErrorFields $value[$key] ($depth + 1)
                    break
                }
            }
        }
        return $selected
    }
    if ($value -is [System.Collections.IEnumerable]) {
        $selected = @()
        foreach ($item in $value) {
            $selected += ,(Select-SafeErrorFields $item ($depth + 1))
        }
        return ,$selected
    }
    return $null
}

function Add-SafeProcessOutput([string]$stage, [string]$stream, $captured) {
    if (!$captured.Text) { return }
    $text = $captured.Text
    $format = "text"
    $parsedJSON = $false
    if (!$captured.Truncated) {
        try {
            $parsed = ConvertFrom-Json -InputObject $text -AsHashtable -ErrorAction Stop
            $parsedJSON = $true
            $selected = Select-SafeErrorFields $parsed
            if ($selected -is [System.Collections.IDictionary] -and $selected.Count -gt 0) {
                $text = ConvertTo-Json -InputObject $selected -Compress -Depth 8
                $format = "json"
            } else {
                $text = "[structured JSON omitted: no standard error fields]"
                $format = "json"
            }
        } catch {
        }
    }
    if (!$parsedJSON) { $text = Redact-DiagnosticText $text }
    $truncated = $captured.Truncated
    if ($text.Length -gt 4096) {
        $text = $text.Substring(0, 4096)
        $truncated = $true
    }
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    $lines = @($text -split "\r?\n")
    foreach ($line in $lines | Select-Object -First 32) {
        if ($line) { Add-Evidence "ProcessOutput=stage=$safeStage,stream=$stream,format=$format,text=$line" }
    }
    if ($lines.Count -gt 32 -or $truncated) {
        Add-Evidence "ProcessOutputTruncated=stage=$safeStage,stream=$stream"
    }
}

function Add-SafeProcessFailure([string]$stage, [string]$kind, [int]$exitCode, [string]$stdoutPath, [string]$stderrPath) {
    $stdout = Read-BoundedProcessOutput $stdoutPath
    $stderr = Read-BoundedProcessOutput $stderrPath
    $combined = "$($stdout.Text)`n$($stderr.Text)"
    $diagnosticClass = "unclassified"
    $categories = @(
        @{ Pattern = '(?i)connection refused'; Name = 'connection-refused' },
        @{ Pattern = '(?i)no route to host|network is unreachable'; Name = 'network-unreachable' },
        @{ Pattern = '(?i)could not resolve|no such host|name resolution'; Name = 'host-resolution-failed' },
        @{ Pattern = '(?i)permission denied|authentication failed|access denied'; Name = 'authentication-or-permission-denied' },
        @{ Pattern = '(?i)timed out|timeout'; Name = 'timeout' },
        @{ Pattern = '(?i)workspace.{0,80}(already exists|invalid|not found)|(already exists|invalid|not found).{0,80}workspace'; Name = 'workspace-state-failure' },
        @{ Pattern = '(?i)provider.{0,80}(failed|unavailable|invalid|not found)'; Name = 'provider-failure' },
        @{ Pattern = '(?i)docker|image|manifest'; Name = 'container-or-image-failure' },
        @{ Pattern = '(?i)error|failed|fatal'; Name = 'application-error' }
    )
    foreach ($category in $categories) {
        if ([regex]::IsMatch($combined, $category.Pattern)) {
            $diagnosticClass = $category.Name
            break
        }
    }
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    Add-Evidence "ProcessFailure=stage=$safeStage,kind=$kind,exitCode=$exitCode,stdoutBytes=$($stdout.Bytes),stderrBytes=$($stderr.Bytes),diagnosticClass=$diagnosticClass"
    Add-SafeProcessOutput $stage "stdout" $stdout
    Add-SafeProcessOutput $stage "stderr" $stderr
}

function Invoke-QuietProcess([string]$path, [string[]]$arguments, [int]$timeoutSeconds, [string]$stage) {
    $run = Start-QuietProcess $path $arguments $stage
    if (!$run.Process.WaitForExit($timeoutSeconds * 1000)) {
        Stop-Process -Id $run.Process.Id -Force -ErrorAction SilentlyContinue
        Add-SafeProcessFailure $stage "timeout" -1 $run.Stdout $run.Stderr
        throw "$stage timed out"
    }
    if ($run.Process.ExitCode -ne 0) {
        Add-SafeProcessFailure $stage "exit" $run.Process.ExitCode $run.Stdout $run.Stderr
        throw "$stage failed with exit $($run.Process.ExitCode)"
    }
    return [System.IO.File]::ReadAllText($run.Stdout)
}

function Get-VisibleCodiumProcesses {
    $items = @()
    foreach ($process in Get-CimInstance Win32_Process -Filter "Name='VSCodium.exe'") {
        $native = Get-Process -Id $process.ProcessId -ErrorAction SilentlyContinue
        if ($native -and $native.MainWindowHandle -ne 0) {
            $items += [pscustomobject]@{
                PID = [int]$process.ProcessId
                ParentPID = [int]$process.ParentProcessId
                SessionId = [int]$native.SessionId
                Window = $true
                TitleHasSSH = ([string]$native.MainWindowTitle -match 'SSH')
            }
        }
    }
    return $items
}

function Write-EditorEvidence([string]$phase) {
    $processes = @(Get-VisibleCodiumProcesses)
    foreach ($process in $processes) {
        Add-Evidence "EditorProcess=VSCodium.exe,phase=$phase,PID=$($process.PID),ParentPID=$($process.ParentPID),SessionId=$($process.SessionId),Window=$($process.Window),TitleHasSSH=$($process.TitleHasSSH)"
    }
    if ($processes.Count -eq 0) {
        Add-Evidence "EditorProcess=VSCodium.exe,phase=$phase,Window=false"
    }
    $window = $processes | Where-Object { $_.SessionId -eq 2 } | Select-Object -First 1
    if (!$window) {
        return $null
    }
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Drawing
    $bounds = [System.Windows.Forms.SystemInformation]::VirtualScreen
    $bitmap = [System.Drawing.Bitmap]::new($bounds.Width, $bounds.Height)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.CopyFromScreen($bounds.Left, $bounds.Top, 0, 0, $bitmap.Size)
    $screenshot = Join-Path $runnerTemp "t0-vscodium-$phase.png"
    $bitmap.Save($screenshot, [System.Drawing.Imaging.ImageFormat]::Png)
    $graphics.Dispose()
    $bitmap.Dispose()
    Add-Evidence "VSCodiumScreenshotCaptured=true,phase=$phase"
    return $window
}

function Get-TaskState([string]$taskID) {
    $json = Invoke-QuietProcess $devsy @("workspace", "task", "list", "--result-format=json") 30 "task-list"
    $states = @($json | ConvertFrom-Json)
    $state = $states | Where-Object { $_.id -eq $taskID } | Select-Object -First 1
    if (!$state) { throw "task status was unavailable" }
    return $state
}

function Get-WorkerPID($state) {
    if ($state.PID -gt 0) { return [int]$state.PID }
    if ($state.Process -and $state.Process.PID -gt 0) { return [int]$state.Process.PID }
    return 0
}

function Start-DetachedUp([string]$phase, [string]$workspaceID, [string]$folder, [bool]$recreate) {
    $arguments = @("workspace", "up", "--detach", "--ide=codium", "--id=$workspaceID", "--no-lockfile", "--result-format=json")
    if ($recreate) { $arguments += "--recreate" }
    $arguments += $folder
    $json = Invoke-QuietProcess $devsy $arguments 120 "submit-$phase" | ConvertFrom-Json
    if ($json.kind -ne "task" -or !$json.id) {
        throw "$phase detached up did not return a task ID"
    }
    Add-Evidence "UpSubmit=detached,phase=$phase,taskID=$($json.id),workspaceID=$workspaceID,recreate=$recreate"
    return [string]$json.id
}

function Start-LifetimeObserver([string]$phase, [string]$taskID, [int]$workerPID, [int]$editorPID) {
    $env:DEVSY_T0_PHASE = $phase
    $env:DEVSY_T0_TASK_ID = $taskID
    $env:DEVSY_T0_WORKER_PID = [string]$workerPID
    $env:DEVSY_T0_EDITOR_PID = [string]$editorPID
    $run = Start-QuietProcess $observerTest @("-test.run=^TestNativeT0ObserveEditorLifetime$", "-test.v") "observe-$phase"
    if (!$run.Process.WaitForExit(360000)) {
        Stop-Process -Id $run.Process.Id -Force -ErrorAction SilentlyContinue
        Add-Evidence "LifetimeObserver=timeout,phase=$phase"
        return $false
    }
    $lines = Get-Content -Path $run.Stdout | Where-Object { $_ -like "*T0 phase=*" }
    foreach ($line in $lines) { Add-Evidence $line.Trim() }
    if ($run.Process.ExitCode -ne 0) {
        Add-Evidence "LifetimeObserver=failed,phase=$phase"
        return $false
    }
    Add-Evidence "LifetimeObserver=passed,phase=$phase"
    return $true
}

function Run-OpenRouteProbe([string]$phase, [string]$workspaceID, [string]$folder) {
    $env:DEVSY_T0_WORKSPACE = $workspaceID
    $env:DEVSY_T0_FOLDER = $folder
    $run = Start-QuietProcess $openTest @("-test.run=^TestT0RealVSCodiumCLIToURIFallback$", "-test.v") "open-$phase"
    if (!$run.Process.WaitForExit(180000)) {
        Stop-Process -Id $run.Process.Id -Force -ErrorAction SilentlyContinue
        Add-Evidence "OpenRouteProbe=timeout,phase=$phase"
        return $false
    }
    $lines = Get-Content -Path $run.Stdout | Where-Object { $_ -like "*VSCodiumVersion=*" }
    foreach ($line in $lines) { Add-Evidence $line.Trim() }
    if ($run.Process.ExitCode -ne 0) {
        Add-Evidence "OpenRouteProbe=failed,phase=$phase"
        return $false
    }
    Add-Evidence "OpenRouteProbe=passed,phase=$phase"
    return $true
}

function Test-WorkspaceSSH([string]$phase, [string]$workspaceID) {
    try {
        $null = Invoke-QuietProcess $devsy @("workspace", "ssh", "--command", "true", "--start-services=false", $workspaceID) 90 "ssh-$phase"
        Add-Evidence "WorkspaceSSH=connected,phase=$phase"
        return $true
    } catch {
        Add-Evidence "WorkspaceSSH=failed,phase=$phase"
        return $false
    }
}

function Run-DetachedPhase([string]$phase, [string]$workspaceID, [string]$folder, [bool]$recreate) {
    $beforePIDs = @(Get-VisibleCodiumProcesses | ForEach-Object { $_.PID })
    $taskID = Start-DetachedUp $phase $workspaceID $folder $recreate
    $state = $null
    $workerPID = 0
    $window = $null
    $deadline = (Get-Date).AddMinutes(15)
    while ((Get-Date) -lt $deadline) {
        $state = Get-TaskState $taskID
        $workerPID = Get-WorkerPID $state
        $visible = @(Get-VisibleCodiumProcesses)
        if ($beforePIDs.Count -gt 0) {
            $window = $visible | Where-Object { $_.PID -in $beforePIDs } | Select-Object -First 1
        }
        if (!$window) { $window = $visible | Where-Object { $_.SessionId -eq 2 } | Select-Object -First 1 }
        if ($workerPID -gt 0 -and $window) { break }
        if ($state.status -in @("succeeded", "failed")) { break }
        Start-Sleep -Milliseconds 500
    }
    if ($workerPID -le 0) {
        Add-Evidence "LifetimeObserver=not-started,phase=$phase,reason=workerPID-unavailable"
    } elseif (!$window) {
        Add-Evidence "LifetimeObserver=not-started,phase=$phase,reason=VSCodium-window-unavailable"
    } else {
        $worker = Get-CimInstance Win32_Process -Filter "ProcessId=$workerPID" -ErrorAction SilentlyContinue
        if ($worker) {
            Add-Evidence "WorkerProcess=$($worker.Name),PID=$workerPID,ParentPID=$($worker.ParentProcessId),SessionId=$($worker.SessionId)"
        }
        Add-Evidence "EditorProcess=VSCodium.exe,phase=$phase,PID=$($window.PID),ParentPID=$($window.ParentPID),SessionId=$($window.SessionId),Window=true"
        Start-LifetimeObserver $phase $taskID $workerPID $window.PID | Out-Null
    }

    $deadline = (Get-Date).AddMinutes(15)
    do {
        $state = Get-TaskState $taskID
        if ($state.status -in @("succeeded", "failed")) { break }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    $workerPID = Get-WorkerPID $state
    Add-Evidence "UpTask=phase-$phase,status=$($state.status),workerPID=$workerPID,durationMs=$($state.durationMs)"
    if ($state.status -ne "succeeded") {
        Add-Evidence "UpTaskResult=not-succeeded,phase=$phase"
        return $false
    }

    $folderPath = [string]$state.result.substitutionContext.containerWorkspaceFolder
    if (!$folderPath) {
        Add-Evidence "WorkspaceFolder=unavailable,phase=$phase"
        return $false
    }
    Add-Evidence "WorkspaceFolder=available,phase=$phase"
    if ($phase -in @("minimal-fresh", "reporter-fresh")) {
        Run-OpenRouteProbe $phase $workspaceID $folderPath | Out-Null
    }
    Test-WorkspaceSSH $phase $workspaceID | Out-Null
    $after = Write-EditorEvidence $phase
    if ($after) { Add-Evidence "EditorWindow=visible,phase=$phase,SessionId=$($after.SessionId),TitleHasSSH=$($after.TitleHasSSH)" }
    else { Add-Evidence "EditorWindow=not-visible,phase=$phase" }
    return $true
}

try {
    Add-Evidence "RunnerInteractive=$([Environment]::UserInteractive),SessionId=$([Diagnostics.Process]::GetCurrentProcess().SessionId),Architecture=$env:PROCESSOR_ARCHITECTURE,Is64BitOS=$([Environment]::Is64BitOperatingSystem)"

    $vscodiumVersion = "1.143.17183"
    $vscodiumZip = Join-Path $runnerTemp "VSCodium-win32-x64-$vscodiumVersion.zip"
    Invoke-WebRequest -Uri "https://github.com/VSCodium/vscodium/releases/download/$vscodiumVersion/VSCodium-win32-x64-$vscodiumVersion.zip" -OutFile $vscodiumZip
    $zipHash = (Get-FileHash -Path $vscodiumZip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($zipHash -ne "5aea75fde6a7aa239f661cfa0eb49c09eda782c7221f3f18840e7e679b5edc06") {
        throw "VSCodium ZIP hash mismatch"
    }
    $vscodiumRoot = Join-Path $runnerTemp "vscodium"
    Expand-Archive -Path $vscodiumZip -DestinationPath $vscodiumRoot -Force
    $cli = Join-Path $vscodiumRoot "bin\codium.cmd"
    $exe = Join-Path $vscodiumRoot "VSCodium.exe"
    if (!(Test-Path $cli) -or !(Test-Path $exe)) { throw "VSCodium ZIP layout mismatch" }
    $env:Path = "$(Split-Path $cli);$env:Path"
    $env:DEVSY_T0_EDITOR_CLI = $cli
    $versionLine = (& $cli --version 2>$null | Select-Object -First 1)
    Add-Evidence "VSCodiumRelease=$vscodiumVersion,VSCodiumZIPSHA256=$zipHash,VSCodiumCLIReportedVersion=$versionLine,SelectedCLI=codium.cmd"

    $extensionVersion = "0.5.0"
    $vsix = Join-Path $runnerTemp "open-remote-ssh-$extensionVersion.vsix"
    Invoke-WebRequest -Uri "https://open-vsx.org/api/jeanp413/open-remote-ssh/$extensionVersion/file/jeanp413.open-remote-ssh-$extensionVersion.vsix" -OutFile $vsix
    $vsixHash = (Get-FileHash -Path $vsix -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($vsixHash -ne "99e72aec520a75e8a6ddb4feccf7353c1ff8e700ca885afe811ba96b6c3b48ca") {
        throw "Open Remote SSH VSIX hash mismatch"
    }
    $installCommand = "& '$cli' --install-extension '$vsix' --force"
    $install = Start-QuietProcess "powershell.exe" @("-NoProfile", "-NonInteractive", "-Command", $installCommand) "install-ssh-extension"
    if (!$install.Process.WaitForExit(90000)) {
        Stop-Process -Id $install.Process.Id -Force -ErrorAction SilentlyContinue
        throw "Open Remote SSH extension install timed out"
    }
    if ($install.Process.ExitCode -ne 0) { throw "Open Remote SSH extension install failed" }
    $extension = ""
    $extensionDeadline = (Get-Date).AddSeconds(60)
    do {
        $listed = & $cli --list-extensions --show-versions 2>$null
        $extension = $listed | Where-Object { $_ -like "jeanp413.open-remote-ssh@*" } | Select-Object -First 1
        if ($extension) { break }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $extensionDeadline)
    if ($extension -ne "jeanp413.open-remote-ssh@$extensionVersion") { throw "Open Remote SSH extension enumeration failed" }
    Add-Evidence "OpenRemoteSSHVSIXSHA256=$vsixHash,OpenRemoteSSHExtension=$extension,Profile=VSCodium-default"

    go build -o $devsy .
    if ($LASTEXITCODE -ne 0) { throw "Windows Devsy build failed" }
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    go build -o (Join-Path $runnerTemp "devsy-linux-amd64") .
    $agentBuildExitCode = $LASTEXITCODE
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
    if ($agentBuildExitCode -ne 0) { throw "Linux agent build failed" }
    go test -c -o $observerTest ./pkg/command
    if ($LASTEXITCODE -ne 0) { throw "Windows command observer build failed" }
    go test -c -o $openTest ./pkg/ide/vscode
    if ($LASTEXITCODE -ne 0) { throw "Windows editor route probe build failed" }
    go build -o $e2eHelper ./e2e/cmd/devsy-ci
    if ($LASTEXITCODE -ne 0) { throw "Windows Docker WSL helper build failed" }
    Add-Evidence "NativeBuilds=devsy.exe,linux-agent,command-observer,editor-route-probe,docker-wsl-helper"

    $agentDir = Join-Path $runnerTemp "agent-files"
    New-Item -ItemType Directory -Force -Path $agentDir | Out-Null
    Copy-Item (Join-Path $runnerTemp "devsy-linux-amd64") (Join-Path $agentDir "devsy-linux-amd64")
    $server = Start-Process -FilePath "python" -ArgumentList @("-m", "http.server", "8080", "--bind", "127.0.0.1", "--directory", $agentDir) -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $runnerTemp "agent-http.stdout") -RedirectStandardError (Join-Path $runnerTemp "agent-http.stderr")
    $env:DEVSY_AGENT_URL = "http://127.0.0.1:8080/"
    $serverDeadline = (Get-Date).AddSeconds(15)
    do {
        try {
            $null = Invoke-WebRequest -Method Head -Uri "$($env:DEVSY_AGENT_URL)devsy-linux-amd64" -TimeoutSec 2
            break
        } catch {
            Start-Sleep -Milliseconds 250
        }
    } while ((Get-Date) -lt $serverDeadline)
    if ((Get-Date) -ge $serverDeadline) { throw "local agent server did not become ready" }

    $null = Invoke-QuietProcess $e2eHelper @("docker-wsl", "setup", "--distribution", "Ubuntu-24.04", "--timeout", "4m", "--state-file", $stateFile) 900 "docker-wsl-setup"
    $fixtureState = Get-Content -Path $stateFile -Raw | ConvertFrom-Json
    $sshAlias = [string]$fixtureState.ssh_host_alias
    Add-KnownDiagnosticSecret ([string]$fixtureState.ssh_endpoint)
    foreach ($path in @($fixtureState.owned.ssh_identity_file, $fixtureState.owned.known_hosts_file)) {
        Add-KnownDiagnosticPath ([string]$path)
    }
    $identityFile = [string]$fixtureState.owned.ssh_identity_file
    if ($identityFile -and (Test-Path -LiteralPath $identityFile)) {
        $privateKey = [System.IO.File]::ReadAllText($identityFile)
        Add-KnownDiagnosticSecret $privateKey.Trim()
        Add-KnownDiagnosticSecret ([Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($privateKey)))
    }
    if (!$sshAlias -or $sshAlias -match '[\s/@]') { throw "Docker WSL SSH fixture alias was unavailable" }
    Add-Evidence "DockerWSLFixture=ready,distribution=Ubuntu-24.04,sshHostAliasPresent=true"

    $null = Invoke-QuietProcess $devsy @("provider", "add", "ssh@v1.1.44", "--option", "HOST=devsyci@$sshAlias") 300 "provider-add-ssh"
    Add-Evidence "SSHProvider=installed,version=1.1.44"

    $minimal = Join-Path $fixtureRoot "minimal-image"
    New-Item -ItemType Directory -Force -Path $minimal | Out-Null
    Set-Content -Path (Join-Path $minimal ".devcontainer.json") -Value '{"name":"Go","image":"ghcr.io/devsy-org/test-images/go:1"}' -Encoding ascii
    $reporter = Join-Path $fixtureRoot "reporter-dockerfile"
    New-Item -ItemType Directory -Force -Path $reporter | Out-Null
    Set-Content -Path (Join-Path $reporter "Dockerfile") -Value "FROM ghcr.io/devsy-org/test-images/go:1" -Encoding ascii
    Set-Content -Path (Join-Path $reporter ".devcontainer.json") -Value '{"name":"Go Dockerfile","build":{"dockerfile":"Dockerfile"}}' -Encoding ascii

    $null = Run-DetachedPhase "minimal-fresh" "t0-minimal" $minimal $false
    $null = Run-DetachedPhase "minimal-reopen" "t0-minimal" $minimal $false
    $null = Run-DetachedPhase "reporter-fresh" "t0-reporter" $reporter $false

    $foregroundStarted = Get-Date
    $arguments = @("workspace", "up", "--ide=codium", "--id=t0-reporter", "--recreate", "--no-lockfile", "--result-format=json", $reporter)
    $run = Start-QuietProcess $devsy $arguments "foreground-recreate"
    if (!$run.Process.WaitForExit(900000)) {
        Stop-Process -Id $run.Process.Id -Force -ErrorAction SilentlyContinue
        Add-Evidence "ForegroundRecreate=timeout"
    } else {
        Add-Evidence "ForegroundRecreate=exit-$($run.Process.ExitCode),elapsedMs=$([int]((Get-Date)-$foregroundStarted).TotalMilliseconds)"
    }
    Test-WorkspaceSSH "foreground-recreate" "t0-reporter" | Out-Null
    $window = Write-EditorEvidence "foreground-recreate"
    if ($window) { Add-Evidence "ForegroundRecreateEditorAlive=true,PID=$($window.PID),SessionId=$($window.SessionId)" }
    else { Add-Evidence "ForegroundRecreateEditorAlive=false" }

    Add-Evidence "T0Run=completed"
} catch {
    Add-Evidence "T0Run=failed,errorClass=$($_.Exception.GetType().Name)"
    throw
} finally {
    if ($server -and !$server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
}
