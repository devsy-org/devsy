[CmdletBinding()]
param([switch]$DiagnosticSelfTest)
$ErrorActionPreference = "Stop"

$root = (Get-Location).Path
$runnerTemp = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [System.IO.Path]::GetTempPath() }
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
        Where-Object { $_.Name -match '(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE[_-]?KEY|ACCESS[_-]?KEY|API[_-]?KEY|APIKEY|AUTHORIZATION)' } |
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

Add-KnownDiagnosticPath $runnerTemp
Add-KnownDiagnosticPath $fixtureRoot
Add-KnownDiagnosticPath $stateFile

function Get-SafeErrorContext($context) {
    if ($context -isnot [System.Collections.IDictionary]) { return [ordered]@{} }
    $safe = [ordered]@{}
    foreach ($key in @($context.Keys | Sort-Object)) {
        $name = [string]$key
        if ($name -match '(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE[_-]?KEY|ACCESS[_-]?KEY|API[_-]?KEY|APIKEY|AUTHORIZATION)') {
            $safe[$name] = "[REDACTED]"
        } elseif ($context[$key] -is [string]) {
            $safe[$name] = Redact-DiagnosticText $context[$key]
        }
    }
    return $safe
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
    $privateKeyDepth = 0
    foreach ($marker in [regex]::Matches($text, '(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----|-----END [A-Z ]*PRIVATE KEY-----')) {
        if ($marker.Value -match '(?i)-----BEGIN') {
            $privateKeyDepth++
        } elseif ($privateKeyDepth -eq 0) {
            return "[diagnostic omitted: incomplete private-key block]"
        } else {
            $privateKeyDepth--
        }
    }
    if ($privateKeyDepth -gt 0) {
        return "[diagnostic omitted: incomplete private-key block]"
    }
    $redactions = @(
        @{ Pattern = '(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)'; Replacement = '-----BEGIN PRIVATE KEY----- [REDACTED] -----END PRIVATE KEY-----' },
        @{ Pattern = '\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b'; Replacement = '[REDACTED]' },
        @{ Pattern = '(?i)(authorization:\s*(?:bearer|basic|token)\s+)\S+'; Replacement = '${1}[REDACTED]' },
        @{ Pattern = '(?i)(https?://[^/\s:@]+:)[^@\s/]+@'; Replacement = '${1}[REDACTED]@' },
        @{ Pattern = '(?i)(?:x-api-key|api-key)\s*:\s*\S+'; Replacement = '[REDACTED]' }
    )
    foreach ($redaction in $redactions) {
        $text = [regex]::Replace($text, $redaction.Pattern, $redaction.Replacement)
    }

    $secretNames = '(?:TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE[_-]?KEY|ACCESS[_-]?KEY|API[_-]?KEY|APIKEY|AUTHORIZATION|(?<![A-Za-z0-9])AUTH(?![A-Za-z0-9]))'
    $secretKey = '(?:"[^"\r\n]*' + $secretNames + '[^"\r\n]*"|''[^''\r\n]*' + $secretNames + '[^''\r\n]*''|[A-Za-z0-9_.-]*' + $secretNames + '[A-Za-z0-9_.-]*)'
    $secretPropertyPattern = '(?im)(?<key>' + $secretKey + ')[\t ]*:[\t ]*(?:"(?:\\.|[^"\\\r\n])*(?:"|(?=\r?$))|''(?:\\.|[^''\\\r\n])*(?:''|(?=\r?$))|(?:\{|\[)[\s\S]*\z|[^\s,}\]]*)'
    $redactSecretProperty = [System.Text.RegularExpressions.MatchEvaluator]{
        param($match)
        return $match.Groups["key"].Value + ':"[REDACTED]"'
    }
    $text = [regex]::Replace($text, $secretPropertyPattern, $redactSecretProperty)
    $text = [regex]::Replace($text, '(?i)\b([A-Za-z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE_KEY|ACCESS_KEY|API_KEY|APIKEY)[A-Za-z0-9_]*)(\s*[=:]\s*)(?!\[REDACTED\])(["'']?)([^"''\s,}\]]+)\3', '${1}${2}${3}[REDACTED]${3}')
    return [regex]::Replace($text, '[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]', '?')
}

function Read-BoundedProcessOutput([string]$path, [switch]$Tail, [int]$Limit = 4096) {
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
            Tail = [bool]$Tail
            DroppedBoundary = $false
        }
    }
    $readLimit = $Limit + [Math]::Max(512, $maxRedactionLength)
    $offset = [long]0
    $truncated = $file.Length -gt $readLimit
    if ($Tail -and $truncated) {
        $offset = $file.Length - $readLimit
    }
    if ($offset -gt 0) {
        return [pscustomobject]@{
            Bytes = $file.Length
            Text = ""
            Truncated = $true
            Tail = $true
            DroppedBoundary = $true
            OmissionReason = "contextless-tail-omitted"
        }
    }
    $share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    $stream = [System.IO.FileStream]::new($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $share)
    $reader = $null
    try {
        if ($offset -gt 0) {
            $null = $stream.Seek($offset, [System.IO.SeekOrigin]::Begin)
        }
        $reader = [System.IO.StreamReader]::new($stream)
        $buffer = [char[]]::new($readLimit)
        $sampleLength = $reader.ReadBlock($buffer, 0, $buffer.Length)
        $hasMore = $reader.Peek() -ge 0
        $text = [string]::new($buffer, 0, $sampleLength)
        $droppedBoundary = $false
        if ($offset -gt 0) {
            $firstLineBreak = $text.IndexOf("`n")
            if ($firstLineBreak -lt 0) {
                $text = ""
            } else {
                $text = $text.Substring($firstLineBreak + 1)
            }
            $droppedBoundary = $true
        }
        if ($hasMore) {
            $lastLineBreak = $text.LastIndexOf("`n")
            if ($lastLineBreak -lt 0) {
                $text = ""
            } else {
                $text = $text.Substring(0, $lastLineBreak + 1)
            }
            $droppedBoundary = $true
            $truncated = $true
        }
    } finally {
        if ($null -ne $reader) {
            $reader.Dispose()
        } else {
            $stream.Dispose()
        }
    }
    return [pscustomobject]@{
        Bytes = $file.Length
        Text = $text
        Truncated = $truncated
        Tail = [bool]$Tail
        DroppedBoundary = $droppedBoundary
        OmissionReason = ""
    }
}

function Select-SafeErrorFields($value, [int]$depth = 0) {
    if ($depth -gt 8) { return "[cause-depth-limit]" }
    if ($null -eq $value -or $value -is [ValueType]) { return $value }
    if ($value -is [string]) { return Redact-DiagnosticText $value }
    if ($value -is [System.Collections.IDictionary]) {
        $selected = [ordered]@{}
        foreach ($name in @("code", "kind", "message", "hint", "cause", "causes", "error", "context")) {
            foreach ($key in $value.Keys) {
                if ([string]$key -ieq $name) {
                    if ($name -ieq "context") {
                        $selected[$key] = Get-SafeErrorContext $value[$key]
                    } else {
                        $selected[$key] = Select-SafeErrorFields $value[$key] ($depth + 1)
                    }
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

function Get-SafeProcessOutput($captured, [switch]$ForceProjectionFailure) {
    $text = $captured.Text
    $format = "text"
    if ($text -and !$captured.Truncated) {
        try {
            $parsed = ConvertFrom-Json -InputObject $text -AsHashtable -ErrorAction Stop
            if ($ForceProjectionFailure) { throw "forced projection failure" }
            $selected = Select-SafeErrorFields $parsed
            if ($selected -is [System.Collections.IDictionary] -and $selected.Count -gt 0) {
                $text = ConvertTo-Json -InputObject $selected -Compress -Depth 8
            } else {
                $text = "[structured JSON omitted: no standard error fields]"
            }
            $format = "json"
        } catch {
            $text = $captured.Text
            $format = "text"
        }
    }
    $text = Redact-DiagnosticText $text
    $truncated = $captured.Truncated
    if ($text.Length -gt 4096) {
        if ($captured.Tail) {
            $text = $text.Substring($text.Length - 4096)
        } else {
            $text = $text.Substring(0, 4096)
        }
        $truncated = $true
    }
    return [pscustomobject]@{
        Text = $text
        Format = $format
        Truncated = $truncated
        OmissionReason = $captured.OmissionReason
    }
}

function Add-SafeProcessOutput([string]$stage, [string]$stream, $captured) {
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    if ($captured.OmissionReason) {
        Add-Evidence "ProcessOutputOmitted=stage=$safeStage,stream=$stream,reason=$($captured.OmissionReason),bytes=$($captured.Bytes)"
        return
    }
    if (!$captured.Text) { return }
    $formatted = Get-SafeProcessOutput $captured
    $lines = @($formatted.Text -split "\r?\n|\r")
    $selectedLines = if ($captured.Tail) { $lines | Select-Object -Last 32 } else { $lines | Select-Object -First 32 }
    foreach ($line in $selectedLines) {
        if ($line) { Add-Evidence "ProcessOutput=stage=$safeStage,stream=$stream,format=$($formatted.Format),text=$line" }
    }
    if ($lines.Count -gt 32 -or $formatted.Truncated) {
        Add-Evidence "ProcessOutputTruncated=stage=$safeStage,stream=$stream"
    }
}
function Add-SafeProcessFileOutput([string]$stage, [string]$stream, [string]$path, [switch]$Tail, [switch]$Sections, [int]$Limit = 4096) {
    if (!(Test-Path -LiteralPath $path)) {
        Add-Evidence "ProcessCaptureUnavailable=stage=$stage,stream=$stream"
        return
    }
    $captured = Read-BoundedProcessOutput $path -Tail:$Tail -Limit $Limit
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    $reason = if ($captured.OmissionReason) { $captured.OmissionReason } else { "none" }
    Add-Evidence "ProcessFileCapture=stage=$safeStage,stream=$stream,bytes=$($captured.Bytes),truncated=$($captured.Truncated),boundaryDropped=$($captured.DroppedBoundary),reason=$reason"
    if ($captured.OmissionReason) {
        Add-Evidence "ProcessFileOmitted=stage=$safeStage,stream=$stream,reason=$($captured.OmissionReason)"
        return
    }
    if ($Sections) {
        $safeSections = @(Get-SafeDiagnosticSections $captured.Text)
        if ($safeSections.Count -eq 0) {
            Add-Evidence "ProcessSectionCapture=stage=$safeStage,status=unavailable,reason=no-complete-section-boundaries,boundaryDropped=$($captured.DroppedBoundary)"
            return
        }
        Add-Evidence "ProcessSectionCapture=stage=$safeStage,status=captured,truncated=$($captured.Truncated),boundaryDropped=$($captured.DroppedBoundary)"
        Add-SafeDiagnosticSections $stage $stream $captured.Text
        return
    }
    Add-SafeProcessOutput $stage $stream $captured
}

function Get-SafeProcessFileOutput([string]$path, [switch]$ForceProjectionFailure, [switch]$Tail, [int]$Limit = 4096) {
    $captured = Read-BoundedProcessOutput $path -Tail:$Tail -Limit $Limit
    if ($ForceProjectionFailure) {
        return Get-SafeProcessOutput $captured -ForceProjectionFailure
    }
    return Get-SafeProcessOutput $captured
}

function Get-SafeDiagnosticSections([string]$text) {
    $safeText = Redact-DiagnosticText $text
    $sections = [ordered]@{}
    $currentSection = ""
    foreach ($line in $safeText -split "\r?\n|\r") {
        if ($line -match '^== tail .*/dockerd\.log') {
            $currentSection = "dockerd"
        } elseif ($line -match '^== tail .*/sshd\.log') {
            $currentSection = "sshd"
        } elseif ($line -match '^== docker socket') {
            $currentSection = "docker-socket"
        } elseif ($line -match '^== processes') {
            $currentSection = "processes"
        } elseif ($line -match '^== listeners .*/proc/net/tcp6') {
            $currentSection = "listeners-tcp6"
        } elseif ($line -match '^== listeners .*/proc/net/tcp') {
            $currentSection = "listeners-tcp"
        } elseif ($line -match '^== ') {
            $currentSection = ""
            continue
        }
        if ($currentSection -and $line -notmatch '^== ') {
            if (!$sections.Contains($currentSection)) {
                $sections[$currentSection] = @()
            }
            $sections[$currentSection] += $line
        }
    }

    $safeSections = @()
    foreach ($name in $sections.Keys) {
        $sectionText = $sections[$name] -join "`n"
        $limit = if ($name -in @("dockerd", "sshd")) { 4096 } else { 1024 }
        $truncated = $sectionText.Length -gt $limit
        if ($truncated) {
            $sectionText = $sectionText.Substring($sectionText.Length - $limit)
        }
        $safeSections += [pscustomobject]@{
            Name = $name
            Text = $sectionText
            Truncated = $truncated
        }
    }
    return $safeSections
}

function Add-SafeDiagnosticSections([string]$stage, [string]$stream, [string]$text) {
    $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
    foreach ($section in (Get-SafeDiagnosticSections $text)) {
        Add-Evidence "ProcessSection=stage=$safeStage,stream=$stream,section=$($section.Name),characters=$($section.Text.Length),truncated=$($section.Truncated)"
        foreach ($line in $section.Text -split "\r?\n|\r") {
            if ($line) {
                Add-Evidence "ProcessOutput=stage=$safeStage,stream=$stream,section=$($section.Name),format=text,text=$line"
            }
        }
    }
}


function Capture-SafeProcessOutput([string]$path, [string[]]$arguments, [int]$timeoutSeconds, [string]$stage, [switch]$Tail) {
    $run = Start-QuietProcess $path $arguments $stage
    $timedOut = $false
    $treeKill = "not-needed"
    $killWaitTimedOut = $false
    try {
        $safeStage = $stage -replace '[^a-zA-Z0-9-]', '-'
        $timedOut = !$run.Process.WaitForExit($timeoutSeconds * 1000)
        if ($timedOut) {
            try {
                $run.Process.Kill($true)
                $treeKill = "requested"
            } catch {
                $treeKill = "unavailable"
                try {
                    $run.Process.Kill()
                    $treeKill = "parent-only"
                } catch {
                    Add-Evidence "ProcessCaptureKill=stage=$safeStage,status=failed,errorClass=$($_.Exception.GetType().Name)"
                }
            }
            $killWaitTimedOut = !$run.Process.WaitForExit(5000)
        }
        $stdout = Read-BoundedProcessOutput $run.Stdout -Tail:$Tail
        $stderr = Read-BoundedProcessOutput $run.Stderr -Tail:$Tail
        $exitCode = if ($timedOut -or $killWaitTimedOut) { -1 } else { $run.Process.ExitCode }
        Add-Evidence "ProcessCapture=stage=$safeStage,exitCode=$exitCode,timeout=$timedOut,treeKill=$treeKill,killWaitTimedOut=$killWaitTimedOut,stdoutBytes=$($stdout.Bytes),stderrBytes=$($stderr.Bytes),stdoutTruncated=$($stdout.Truncated),stderrTruncated=$($stderr.Truncated),stdoutBoundaryDropped=$($stdout.DroppedBoundary),stderrBoundaryDropped=$($stderr.DroppedBoundary)"
        Add-SafeProcessOutput $stage "stdout" $stdout
        Add-SafeProcessOutput $stage "stderr" $stderr
        return [pscustomobject]@{
            Succeeded = !$timedOut -and !$killWaitTimedOut -and $run.Process.ExitCode -eq 0
            TimedOut = $timedOut
            ExitCode = $exitCode
            TreeKill = $treeKill
            KillWaitTimedOut = $killWaitTimedOut
        }
    } finally {
        $run.Process.Dispose()
        foreach ($rawCapture in @(
                @{ Stream = "stdout"; Path = $run.Stdout },
                @{ Stream = "stderr"; Path = $run.Stderr }
            )) {
            try {
                if (Test-Path -LiteralPath $rawCapture.Path) {
                    Remove-Item -LiteralPath $rawCapture.Path -Force -ErrorAction Stop
                }
                if (Test-Path -LiteralPath $rawCapture.Path) {
                    throw "raw process capture file remains"
                }
                Add-Evidence "ProcessCaptureRawFile=stream=$($rawCapture.Stream),status=deleted"
            } catch {
                Add-Evidence "ProcessCaptureRawFile=stream=$($rawCapture.Stream),status=deletion-failed,errorClass=$($_.Exception.GetType().Name)"
            }
        }
    }
}

function Capture-FirstTaskFailureDiagnostics([string]$taskID) {
    Add-KnownDiagnosticSecret $taskID
    try {
        $taskLogArgs = @("workspace", "task", "logs", $taskID, "--follow", "--interval=500ms", "--result-format=json")
        $taskLogs = Capture-SafeProcessOutput $devsy $taskLogArgs 30 "task-logs" -Tail
        Add-Evidence "TaskLogsCapture=status=$(if ($taskLogs.Succeeded) { 'success' } elseif ($taskLogs.TimedOut) { 'timeout' } else { 'failed' }),exitCode=$($taskLogs.ExitCode)"
    } catch {
        Add-Evidence "TaskLogsCapture=status=unavailable,errorClass=$($_.Exception.GetType().Name)"
    }

    $diagnosticFile = Join-Path (Split-Path $stateFile) "docker-wsl-diagnostics.log"
    try {
        $cleanupArgs = @("docker-wsl", "cleanup", "--state-file", $stateFile, "--timeout", "2m")
        $cleanup = Capture-SafeProcessOutput $e2eHelper $cleanupArgs 150 "docker-wsl-failure-cleanup"
        $stateRetained = Test-Path -LiteralPath $stateFile
        $outerCleanup = if ($stateRetained) { "pending" } else { "not-needed" }
        Add-Evidence "DockerWSLCleanup=status=$(if ($cleanup.Succeeded) { 'completed' } elseif ($cleanup.TimedOut) { 'timeout' } else { 'failed' }),exitCode=$($cleanup.ExitCode),stateRetained=$stateRetained,outerCleanup=$outerCleanup"
    } catch {
        $stateRetained = Test-Path -LiteralPath $stateFile
        $outerCleanup = if ($stateRetained) { "pending" } else { "not-needed" }
        Add-Evidence "DockerWSLCleanup=status=unavailable,errorClass=$($_.Exception.GetType().Name),stateRetained=$stateRetained,outerCleanup=$outerCleanup"
    }
    if (Test-Path -LiteralPath $diagnosticFile) {
        Add-SafeProcessFileOutput "docker-wsl-failure-diagnostics" "stdout" $diagnosticFile -Tail -Sections -Limit 24576
    } else {
        Add-Evidence "DockerWSLDiagnostics=unavailable,reason=sidecar-missing"
    }
}

function Invoke-DiagnosticSelfTest {
    $testToken = "ghp_selftestToken12345678901234567890"
    $testPassword = "selftest-password-SENTINEL"
    $testApiKey = "selftest-api-key-SENTINEL"
    $testPrivateKey = "selftest-private-key-SENTINEL"
    $testEndpoint = "ssh://selftest-user@fixture-host:2222"
    $testPath = Join-Path $runnerTemp "diagnostic-private-key-path"
    $unregisteredSecret = "unregistered-api-key-SENTINEL"
    foreach ($secret in @($testToken, $testPassword, $testApiKey, $testPrivateKey, $testEndpoint)) {
        Add-KnownDiagnosticSecret $secret
    }
    Add-KnownDiagnosticPath $testPath

    $ordinary = Get-SafeProcessOutput ([pscustomobject]@{
        Text = "Error: detached worker launch failed: access denied opening task state"
        Truncated = $false
    })
    if (!$ordinary.Text.Contains("detached worker launch failed") -or !$ordinary.Text.Contains("access denied")) {
        throw "Diagnostic self-test failed: ordinary cause was lost"
    }

    $structuredRaw = ConvertTo-Json -InputObject @{
        kind = "error"
        outcome = "error"
        code = "E_SELFTEST"
        message = "detached worker failed with TOKEN=$testToken"
        causes = @(@{ message = "connection refused at $testEndpoint" })
        password = $testPassword
        UNREGISTERED_API_KEY = $unregisteredSecret
    } -Compress
    $structured = Get-SafeProcessOutput ([pscustomobject]@{
        Text = $structuredRaw
        Truncated = $false
    })
    if (!$structured.Text.Contains("E_SELFTEST") -or !$structured.Text.Contains("detached worker failed") -or !$structured.Text.Contains("connection refused")) {
        throw "Diagnostic self-test failed: structured cause was lost"
    }
    if ($structured.Text.Contains($testToken) -or $structured.Text.Contains($testPassword) -or $structured.Text.Contains($testEndpoint)) {
        throw "Diagnostic self-test failed: structured secret escaped"
    }
    if ($structured.Text.Contains($unregisteredSecret)) {
        throw "Diagnostic self-test failed: unregistered structured secret escaped"
    }
    $rawSecretRecord = '{"message":"retained SSH cause","UNREGISTERED_API_KEY":"' + $unregisteredSecret + '"}'
    foreach ($capturedText in @(
            $rawSecretRecord,
            ($rawSecretRecord + "`n" + '{"message":"later cause"}'),
            ('{"message":"retained SSH cause","UNREGISTERED_API_KEY":"' + $unregisteredSecret)
        )) {
        $rawFallback = Get-SafeProcessOutput ([pscustomobject]@{
            Text = $capturedText
            Truncated = $true
        })
        if (!$rawFallback.Text.Contains("retained SSH cause") -or
            $rawFallback.Text.Contains($unregisteredSecret) -or
            !$rawFallback.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
            throw "Diagnostic self-test failed: truncated or malformed secret property escaped"
        }
    }

    $secretText = @"
-----BEGIN OPENSSH PRIVATE KEY-----
$testPrivateKey
-----END OPENSSH PRIVATE KEY-----
authorization: Bearer $testToken
TOKEN=$testToken
PASSWORD=$testPassword
API_KEY=$testApiKey
apikey=$testApiKey
X-API-Key: $testApiKey
url=https://fixture-user:$testPassword@registry.local/v2
path=$testPath
"@
    $secrets = Get-SafeProcessOutput ([pscustomobject]@{
        Text = $secretText
        Truncated = $false
    })
    foreach ($secret in @($testToken, $testPassword, $testApiKey, $testPrivateKey, $testPath)) {
        if ($secrets.Text.Contains($secret)) {
            throw "Diagnostic self-test failed: known secret escaped"
        }
    }
    if (!$secrets.Text.Contains("API_KEY=[REDACTED]") -or !$secrets.Text.Contains("apikey=[REDACTED]")) {
        throw "Diagnostic self-test failed: API key form was not redacted"
    }

    $malformed = Get-SafeProcessOutput ([pscustomobject]@{
        Text = "{`"error`":{`"code`":`"E_MALFORMED`",`"message`":`"worker failed at $testPath; API_KEY=$testApiKey"
        Truncated = $false
    })
    if (!$malformed.Text.Contains("E_MALFORMED") -or !$malformed.Text.Contains("worker failed") -or $malformed.Text.Contains($testApiKey) -or $malformed.Text.Contains($testPath)) {
        throw "Diagnostic self-test failed: malformed JSON was not safely retained"
    }

    $forcedProjection = Get-SafeProcessOutput ([pscustomobject]@{
        Text = $structuredRaw
        Truncated = $false
    }) -ForceProjectionFailure
    if (!$forcedProjection.Text.Contains("detached worker failed") -or
        !$forcedProjection.Text.Contains("connection refused") -or
        $forcedProjection.Text.Contains($testToken) -or
        $forcedProjection.Text.Contains($testEndpoint) -or
        $forcedProjection.Text.Contains($unregisteredSecret) -or
        !$forcedProjection.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
        throw "Diagnostic self-test failed: projection failure bypassed redaction"
    }

    $boundaryFile = Join-Path $runnerTemp "t0-redactor-selftest.txt"
    try {
        [System.IO.File]::WriteAllText($boundaryFile, ("x" * 4090) + $testPath)
        $boundary = Get-SafeProcessOutput (Read-BoundedProcessOutput $boundaryFile)
        if ($boundary.Text.Contains($testPath) -or !$boundary.Truncated) {
            throw "Diagnostic self-test failed: truncation boundary leaked a path"
        }
    } finally {
        Remove-Item -LiteralPath $boundaryFile -Force -ErrorAction SilentlyContinue
    }

    $captureFile = Join-Path $runnerTemp "t0-process-output-selftest.txt"
    $unregisteredCredential = "unregistered-api-key-SENTINEL"
    $capturePayload = @{
        error = @{
            code = "E_CAPTURE"
            message = "managed SSH handshake failed at $testPath with TOKEN=$testToken"
            context = @{
                UNREGISTERED_API_KEY = $unregisteredCredential
                detail = "peer closed before SSH identification"
            }
        }
    }
    try {
        [System.IO.File]::WriteAllText($captureFile, (ConvertTo-Json -InputObject $capturePayload -Compress -Depth 5))
        $capturedOutput = Get-SafeProcessFileOutput $captureFile
        if (!$capturedOutput.Text.Contains("managed SSH handshake failed") -or
            !$capturedOutput.Text.Contains("peer closed before SSH identification") -or
            $capturedOutput.Text.Contains($testToken) -or
            $capturedOutput.Text.Contains($testPath) -or
            $capturedOutput.Text.Contains($unregisteredCredential) -or
            !$capturedOutput.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
            throw "Diagnostic self-test failed: captured process output was not safely retained"
        }
        $forcedCapture = Get-SafeProcessFileOutput $captureFile -ForceProjectionFailure
        if (!$forcedCapture.Text.Contains("managed SSH handshake failed") -or
            !$forcedCapture.Text.Contains("peer closed before SSH identification") -or
            $forcedCapture.Text.Contains($testToken) -or
            $forcedCapture.Text.Contains($testPath) -or
            $forcedCapture.Text.Contains($unregisteredCredential) -or
            !$forcedCapture.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
            throw "Diagnostic self-test failed: captured output projection fallback was unsafe"
        }

        [System.IO.File]::WriteAllText($captureFile, ("x" * 4090) + "API_KEY=$testApiKey" + ("z" * 1024))
        $captureBoundary = Get-SafeProcessFileOutput $captureFile
        if ($captureBoundary.Text.Contains($testApiKey) -or !$captureBoundary.Truncated) {
            throw "Diagnostic self-test failed: captured output truncation boundary leaked a secret"
        }
        $maxRedactionLength = 0
        foreach ($value in @($script:knownOutputSecrets) + @($script:knownOutputPaths)) {
            $maxRedactionLength = [Math]::Max($maxRedactionLength, $value.Length)
        }
        $readLimit = 4096 + [Math]::Max(512, $maxRedactionLength)
        $unregisteredPAT = "ghp_unregisteredBoundaryToken123456789012345"
        $boundaryCause = "E_BOUNDARY: retained application cause`n"
        $propertyPrefix = '"UNREGISTERED_API_KEY":"'
        $paddingLength = $readLimit - $boundaryCause.Length - $propertyPrefix.Length - [int][Math]::Floor($unregisteredPAT.Length / 2)
        $patStart = $boundaryCause.Length + $paddingLength + $propertyPrefix.Length
        if ($patStart -ge $readLimit -or ($patStart + $unregisteredPAT.Length) -le $readLimit) {
            throw "Diagnostic self-test failed: PAT did not straddle the reader limit"
        }
        $patBoundaryRaw = $boundaryCause + ("x" * $paddingLength) + $propertyPrefix + $unregisteredPAT + '"' + "`n"
        [System.IO.File]::WriteAllText($captureFile, $patBoundaryRaw)
        $patBoundary = Get-SafeProcessFileOutput $captureFile
        if (!$patBoundary.Text.Contains("retained application cause") -or
            $patBoundary.Text.Contains($unregisteredPAT) -or
            $patBoundary.Text.Contains($propertyPrefix) -or
            $patBoundary.Text.Contains($unregisteredPAT.Substring(0, 10)) -or
            !$patBoundary.Truncated) {
            throw "Diagnostic self-test failed: unregistered PAT crossed the read limit unsafely"
        }

        $sshCausePayload = @{ error = @{ code = "SSH_EOF"; message = "ClientFromManagedConn: EOF" } }
        [System.IO.File]::WriteAllText($captureFile, (ConvertTo-Json -InputObject $sshCausePayload -Compress -Depth 4))
        $evidenceBeforeCause = [System.IO.File]::ReadAllText($report)
        Add-SafeProcessFileOutput "selftest-ssh-cause" "stderr" $captureFile
        $evidenceAfterCause = [System.IO.File]::ReadAllText($report)
        $causeEvidence = $evidenceAfterCause.Substring($evidenceBeforeCause.Length)
        if (!$causeEvidence.Contains("ClientFromManagedConn: EOF") -or !$causeEvidence.Contains("SSH_EOF")) {
            throw "Diagnostic self-test failed: safe task failure cause was omitted from evidence"
        }
        $longCause = "TERMINAL_SAFE_CAUSE=worker returned connection EOF"
        $longLine = Get-SafeProcessOutput ([pscustomobject]@{
            Text = ConvertTo-Json -InputObject @{ message = ("x" * 5000) + $longCause } -Compress
            Truncated = $false
            Tail = $true
        })
        if (!$longLine.Text -or !$longLine.Text.Contains($longCause)) {
            throw "Diagnostic self-test failed: final clipping erased a long-line cause"
        }

        $pemCanary = "unregistered-pem-material-SENTINEL"
        $helperSectionsRaw = @"
== tail /tmp/devsy-ci/dockerd.log (up to 8 complete 1KiB lines; PEM omitted)
dockerd cause=daemon startup failed
== tail /tmp/devsy-ci/sshd.log (up to 8 complete 1KiB lines; PEM omitted)
UNREGISTERED_API_KEY=$unregisteredSecret
$("x" * 5000) SSHD_TERMINAL_CAUSE=client closed before identification
-----BEGIN OPENSSH PRIVATE KEY-----
$pemCanary
-----END OPENSSH PRIVATE KEY-----
== docker socket
socket present
== processes (pid,ppid,user,comm; max 6)
sshd process present
== listeners /proc/net/tcp (ssh=08AE,docker-tcp=0947; max 4)
ssh listener present
"@
        $helperSections = @(Get-SafeDiagnosticSections $helperSectionsRaw)
        $sectionNames = @($helperSections | ForEach-Object { $_.Name })
        foreach ($name in @("dockerd", "sshd", "docker-socket", "processes", "listeners-tcp")) {
            if ($name -notin $sectionNames) {
                throw "Diagnostic self-test failed: helper section $name was lost"
            }
        }
        $sshdSection = $helperSections | Where-Object { $_.Name -eq "sshd" } | Select-Object -First 1

        $causePresent = $sshdSection.Text.Contains("SSHD_TERMINAL_CAUSE=client closed before identification")
        $secretPresent = $sshdSection.Text.Contains($unregisteredSecret)
        $pemPresent = $sshdSection.Text.Contains($pemCanary)
        $secretFragmentPresent = $sshdSection.Text.Contains($unregisteredSecret.Substring(0, 10))
        if (!$causePresent -or $secretPresent -or $pemPresent -or $secretFragmentPresent) {
            throw "Diagnostic self-test failed: section cause=$causePresent,secret=$secretPresent,pem=$pemPresent,fragment=$secretFragmentPresent,characters=$($sshdSection.Text.Length)"
        }
    } finally {
        Remove-Item -LiteralPath $captureFile -Force -ErrorAction SilentlyContinue
    }
    $unregisteredApiKey = "unregistered-api-key-SENTINEL"
    $taskState = @{
        status = "failed"
        command = "workspace up"
        error = "detached worker failed at $testPath with TOKEN=$testToken"
        errorCode = "docker_daemon_unreachable"
        errorHint = "check $testPath with API_KEY=$testApiKey"
        errorContext = @{
            endpoint = $testEndpoint
            privatePath = $testPath
            token = $testToken
            UNREGISTERED_API_KEY = $unregisteredApiKey
            detail = "retained safe task context"
        }
        config = @{ secret = $testPassword }
    }
    $taskFailure = Get-SafeTaskFailure $taskState
    if (!$taskFailure.Text.Contains("docker_daemon_unreachable") -or
        !$taskFailure.Text.Contains("detached worker failed") -or
        !$taskFailure.Text.Contains("check") -or
        !$taskFailure.Text.Contains("retained safe task context")) {
        throw "Diagnostic self-test failed: task failure fields were lost"
    }
    foreach ($secret in @($testToken, $testPassword, $testApiKey, $testEndpoint, $testPath)) {
        if ($taskFailure.Text.Contains($secret)) {
            throw "Diagnostic self-test failed: task failure secret escaped"
        }
    }
    if (!$taskFailure.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
        throw "Diagnostic self-test failed: unregistered context key was not masked"
    }
    if ($taskFailure.Text.Contains("workspace up") -or $taskFailure.Text.Contains("config")) {
        throw "Diagnostic self-test failed: task state fields outside the projection escaped"
    }
    $forcedTaskFailure = Get-SafeTaskFailure $taskState -ForceProjectionFailure
    if (!$forcedTaskFailure.Text.Contains("retained safe task context") -or
        $forcedTaskFailure.Text.Contains($testToken) -or
        $forcedTaskFailure.Text.Contains($testApiKey) -or
        $forcedTaskFailure.Text.Contains($testEndpoint) -or
        $forcedTaskFailure.Text.Contains($testPath) -or
        $forcedTaskFailure.Text.Contains($unregisteredApiKey) -or
        !$forcedTaskFailure.Text.Contains('"UNREGISTERED_API_KEY":"[REDACTED]"')) {
        throw "Diagnostic self-test failed: task projection failure bypassed redaction"
    }

    $escapedJsonRaw = '{"message":"retained SSH cause","api_key":"prefix\"SECRET_SUFFIX"}'
    $escapedJsonOutput = Get-SafeProcessOutput ([pscustomobject]@{
        Text = $escapedJsonRaw
        Truncated = $false
    }) -ForceProjectionFailure
    $escapedJsonSafe = $escapedJsonOutput.Text.Contains("retained SSH cause") -and
        !$escapedJsonOutput.Text.Contains("SECRET_SUFFIX")

    $pemBoundaryFile = Join-Path $runnerTemp "t0-pem-tail-selftest.txt"
    $pemBodyCanary = "UNREGISTERED_PEM_FILE_BODY_FRAGMENT_1234567890"
    $pemBodyLine = "$pemBodyCanary`n"
    $pemBodyLines = [int][Math]::Ceiling(($readLimit + 1024) / $pemBodyLine.Length)
    $pemBoundaryRaw = "safe header`n-----BEGIN OPENSSH PRIVATE KEY-----`n" +
        ($pemBodyLine * $pemBodyLines) + "-----END OPENSSH PRIVATE KEY-----`n" +
        "PEM_SAFE_TERMINAL_CAUSE=client closed before SSH identification`n"
    $pemBoundarySafe = $false
    try {
        [System.IO.File]::WriteAllText($pemBoundaryFile, $pemBoundaryRaw)
        $pemBoundaryOutput = Get-SafeProcessFileOutput $pemBoundaryFile -Tail
        $evidenceBeforePEM = [System.IO.File]::ReadAllText($report)
        Add-SafeProcessFileOutput "selftest-pem-tail" "stderr" $pemBoundaryFile -Tail
        $evidenceAfterPEM = [System.IO.File]::ReadAllText($report)
        $pemEvidence = $evidenceAfterPEM.Substring($evidenceBeforePEM.Length)
        $pemBoundarySafe = !$pemBoundaryOutput.Text -and
            $pemBoundaryOutput.OmissionReason -eq "contextless-tail-omitted" -and
            $pemEvidence.Contains("reason=contextless-tail-omitted") -and
            !$pemEvidence.Contains($pemBodyCanary) -and
            !$pemEvidence.Contains($pemBodyCanary.Substring(0, 16)) -and
            !$pemEvidence.Contains("PEM_SAFE_TERMINAL_CAUSE")

        foreach ($pemText in @(
                "-----BEGIN OPENSSH PRIVATE KEY-----`n$($pemBodyCanary)`n-----END OPENSSH PRIVATE KEY-----`n",
                "$($pemBodyCanary)`n-----END OPENSSH PRIVATE KEY-----`n",
                "-----BEGIN OPENSSH PRIVATE KEY-----`n$($pemBodyCanary)`n"
            )) {
            [System.IO.File]::WriteAllText($pemBoundaryFile, $pemText)
            $redactedPEM = Get-SafeProcessFileOutput $pemBoundaryFile
            Add-SafeProcessFileOutput "selftest-pem-block" "stderr" $pemBoundaryFile
            if ($redactedPEM.Text.Contains($pemBodyCanary) -or
                $redactedPEM.Text.Contains($pemBodyCanary.Substring(0, 16))) {
                $pemBoundarySafe = $false
            }
        }
        $inlinePEM = ConvertTo-Json -InputObject @{
            message = "-----BEGIN OPENSSH PRIVATE KEY----- $pemBodyCanary -----END OPENSSH PRIVATE KEY-----"
            cause = "ClientFromManagedConn: EOF"
        } -Compress
        $inlinePEMSafe = Get-SafeProcessOutput ([pscustomobject]@{
            Text = $inlinePEM
            Truncated = $false
        })
        if (!$inlinePEMSafe.Text.Contains("ClientFromManagedConn: EOF") -or
            $inlinePEMSafe.Text.Contains($pemBodyCanary)) {
            $pemBoundarySafe = $false
        }
        $pemEvidenceAll = [System.IO.File]::ReadAllText($report)
        if ($pemEvidenceAll.Contains($pemBodyCanary) -or
            $pemEvidenceAll.Contains($pemBodyCanary.Substring(0, 16))) {
            $pemBoundarySafe = $false
        }
    } finally {
        Remove-Item -LiteralPath $pemBoundaryFile -Force -ErrorAction SilentlyContinue
    }
    Write-Host "PrivacyCanaries=escapedJsonValueSafe:$escapedJsonSafe,tailPEMBodySafe:$pemBoundarySafe"
    if (!$escapedJsonSafe -or !$pemBoundarySafe) {
        throw "Diagnostic self-test failed: privacy canary escaped or safe cause was not retained"
    }
    Write-Host "DiagnosticSelfTest=passed"
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
    $states = @($json | ConvertFrom-Json -AsHashtable)
    $state = $states | Where-Object { $_.id -eq $taskID } | Select-Object -First 1
    if (!$state) { throw "task status was unavailable" }
    return $state
}

function Get-SafeTaskFailure($state, [switch]$ForceProjectionFailure) {
    $message = [string]$state.error
    if (!$message) { $message = "task failed without a recorded error" }
    $failure = [ordered]@{
        code = [string]$state.errorCode
        message = $message
        hint = [string]$state.errorHint
        context = Get-SafeErrorContext $state.errorContext
    }
    $captured = [pscustomobject]@{
        Text = (ConvertTo-Json -InputObject $failure -Compress -Depth 5)
        Truncated = $false
    }
    if ($ForceProjectionFailure) {
        return Get-SafeProcessOutput $captured -ForceProjectionFailure
    }
    return Get-SafeProcessOutput $captured
}

function Add-SafeTaskFailure([string]$phase, $state) {
    $formatted = Get-SafeTaskFailure $state
    $safePhase = $phase -replace '[^a-zA-Z0-9-]', '-'
    Add-Evidence "UpTaskFailure=phase=$safePhase,format=$($formatted.Format),text=$($formatted.Text)"
    if ($formatted.Truncated) { Add-Evidence "UpTaskFailureTruncated=phase=$safePhase" }
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
        if ($state.status -eq "failed") {
            Add-SafeTaskFailure $phase $state
            if ($phase -eq "minimal-fresh") {
                Capture-FirstTaskFailureDiagnostics $taskID
            }
        }
        Add-Evidence "UpTaskResult=not-succeeded,phase=$phase"
        if ($phase -eq "minimal-fresh") {
            throw "first minimal workspace up task failed; see safe task diagnostic"
        }
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

if ($DiagnosticSelfTest) {
    Invoke-DiagnosticSelfTest
    return
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
    $upHelp = Invoke-QuietProcess $devsy @("workspace", "up", "--help") 30 "workspace-up-help"
    if ($upHelp -notmatch '(?m)^\s*USAGE:\s*$' -or $upHelp -notmatch '(?m)^\s*devsy workspace up \[flags\]') {
        throw "workspace up CLI help guard failed"
    }
    Add-Evidence "WorkspaceUpHelpGuard=passed,route=devsy workspace up"

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
    Add-KnownDiagnosticSecret $sshAlias
    foreach ($path in @(
            $fixtureState.owned.ssh_identity_file,
            $fixtureState.owned.known_hosts_file,
            $fixtureState.owned.ssh_config,
            $fixtureState.owned.ssh_config_backup,
            $fixtureState.owned.ssh_config_acl_backup
        )) {
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
