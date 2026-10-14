param(
    [Parameter(Mandatory = $true)]
    [string]$ArtifactsDirectory
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false

$jsonPath = Join-Path $ArtifactsDirectory 'provider-update-windows.jsonl'
$exitPath = Join-Path $ArtifactsDirectory 'provider-update-windows.exitcode'
& go test -race -json -count=1 -timeout 10m ./pkg/workspace ./pkg/provider -run '^(TestProviderUpdate|TestStatUpdatePath|TestExtractConfinedUpdateTarExpandsGNUSparseExecutable|TestGNUArchiveFixtureHasExpandedSparseData|TestExtractConfinedUpdateTarPreservesContiguousFiles)' |
  Tee-Object -FilePath $jsonPath
$testExitCode = $LASTEXITCODE
Set-Content -LiteralPath $exitPath -Value $testExitCode -NoNewline

if ($testExitCode -ne 0) {
  Write-Host "::error::Provider update tests failed with exit code $testExitCode"
  exit $testExitCode
}

$events = @(
  Get-Content -LiteralPath $jsonPath |
    ForEach-Object { try { ConvertFrom-Json -InputObject $_ -ErrorAction Stop } catch { $null } } |
    Where-Object { $null -ne $_ }
)
$criticalTests = @(
  'TestProviderUpdateZipCompatibility/plain_archive',
  'TestProviderUpdateZipCompatibility/contained_file_symlink',
  'TestProviderUpdateTarContainedDirectoryLinkThenRegularMember',
  'TestProviderUpdateZipContainedDirectoryLinkThenRegularMember',
  'TestProviderUpdateContainedSeedDirectoryLinkChain',
  'TestProviderUpdateArchivePreservesOwnedDaemon',
  'TestExtractConfinedUpdateTarExpandsGNUSparseExecutable',
  'TestGNUArchiveFixtureHasExpandedSparseData',
  'TestExtractConfinedUpdateTarPreservesContiguousFiles',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/unchanged_managed_target_succeeds',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/changed_managed_target_with_matching_new_checksum_succeeds',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/replaced_managed_symlink_with_matching_new_checksum_succeeds',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/new_managed_target_with_matching_checksum_succeeds',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/changed_managed_target_rejects_and_preserves_live_state',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/contained_symlink_follows_changed_staged_target',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/external_symlink_into_managed_tree_fails_closed',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/case-variant_managed_path_cannot_bypass_validation',
  'TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/outside_reference_remains_untouched_while_managed_tree_updates',
  'TestStatUpdatePath/nested_dot_directory',
  'TestStatUpdatePath/nested_cancelled_child',
  'TestStatUpdatePath/external_target',
  'TestStatUpdatePath/cycle'
)
$status = 0
foreach ($testName in $criticalTests) {
  $testEvents = @($events | Where-Object { $_.Test -eq $testName })
  if ($testEvents.Count -eq 0) {
    Write-Host "::error::Required Windows provider update test produced no JSON events: $testName"
    $status = 1
    continue
  }
  if (@($testEvents | Where-Object { $_.Action -eq 'skip' }).Count -gt 0) {
    Write-Host "::error::Required Windows provider update test was skipped: $testName"
    $status = 1
    continue
  }
  if (@($testEvents | Where-Object { $_.Action -eq 'pass' }).Count -eq 0) {
    Write-Host "::error::Required Windows provider update test did not pass: $testName"
    $status = 1
  }
}
exit $status
