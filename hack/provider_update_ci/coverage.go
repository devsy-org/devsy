package main

import "fmt"

var criticalTests = []string{
	"TestProviderUpdateZipCompatibility/plain_archive",
	"TestProviderUpdateZipCompatibility/contained_file_symlink",
	"TestProviderUpdateTarContainedDirectoryLinkThenRegularMember",
	"TestProviderUpdateZipContainedDirectoryLinkThenRegularMember",
	"TestProviderUpdateContainedSeedDirectoryLinkChain",
	"TestProviderUpdateArchivePreservesOwnedDaemon",
	"TestExtractConfinedUpdateTarExpandsGNUSparseExecutable",
	"TestGNUArchiveFixtureHasExpandedSparseData",
	"TestExtractConfinedUpdateTarPreservesContiguousFiles",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/unchanged_managed_target_succeeds",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/changed_managed_target_with_matching_new_checksum_succeeds",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/" +
		"replaced_managed_symlink_with_matching_new_checksum_succeeds",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/new_managed_target_with_matching_checksum_succeeds",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/changed_managed_target_rejects_and_preserves_live_state",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/contained_symlink_follows_changed_staged_target",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/external_symlink_into_managed_tree_fails_closed",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/case-variant_managed_path_cannot_bypass_validation",
	"TestProviderUpdateAbsoluteReferenceUsesFinalStagedTarget/" +
		"outside_reference_remains_untouched_while_managed_tree_updates",
	"TestStatUpdatePath/nested_dot_directory",
	"TestStatUpdatePath/nested_cancelled_child",
	"TestStatUpdatePath/external_target",
	"TestStatUpdatePath/cycle",
}

type testState struct {
	passed  bool
	skipped bool
	failed  bool
}

func validateCriticalTests(events map[string]testState) []string {
	var issues []string
	for _, name := range criticalTests {
		state, ok := events[name]
		switch {
		case !ok:
			issues = append(issues, fmt.Sprintf(
				"required Windows provider update test produced no JSON events: %s", name,
			))
		case state.skipped:
			issues = append(issues, fmt.Sprintf(
				"required Windows provider update test was skipped: %s", name,
			))
		case state.failed:
			issues = append(issues, fmt.Sprintf(
				"required Windows provider update test failed: %s", name,
			))
		case !state.passed:
			issues = append(issues, fmt.Sprintf(
				"required Windows provider update test did not pass: %s", name,
			))
		}
	}
	return issues
}
