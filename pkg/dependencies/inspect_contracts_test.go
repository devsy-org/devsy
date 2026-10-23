package dependencies_test

import (
	"os"
	"path/filepath"
)

// TestInspectShippedCommandContracts validates retained actual-main stdout with
// the unchanged raw-schema and semantic validators. The producer is the Unix
// PTY process test in cmd, not an authored report fixture.
func (s *contractsSuite) TestInspectShippedCommandContracts() {
	dir := os.Getenv("DEVSY_T10_EVIDENCE_DIR")
	if dir == "" {
		s.T().Skip("requires retained actual-main inspect process outputs")
	}
	for _, builtin := range []string{"docker", "podman", "apple", "kubernetes"} {
		// #nosec G304 G703 -- explicit runner-owned evidence directory and fixed producer filenames.
		raw, err := os.ReadFile(filepath.Join(dir, "report-"+builtin+".stdout"))
		s.Require().NoError(err)
		report, err := s.decodeAndValidateRaw(raw)
		s.Require().NoError(err)
		s.Equal(builtin, report.Target.ProviderType)
		s.Empty(report.Actions)
	}
}
