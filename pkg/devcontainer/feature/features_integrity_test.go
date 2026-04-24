package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/stretchr/testify/suite"
)

type IntegrityTestSuite struct {
	suite.Suite
}

func TestIntegrityTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrityTestSuite))
}

// createTestTarball writes a fake tarball file and returns its path.
func createTestTarball(dir string) (string, error) {
	tarball := filepath.Join(dir, "feature.tgz")
	return tarball, os.WriteFile(tarball, []byte("fake-tarball-content"), 0o600)
}

func (s *IntegrityTestSuite) TestStoreIntegrityHash_WritesCorrectSidecar() {
	dir := s.T().TempDir()
	tarball, err := createTestTarball(dir)
	s.Require().NoError(err)

	storeIntegrityHash(dir, tarball, "test-feature")

	hashFile := filepath.Join(dir, "feature.sha256")
	stored, err := os.ReadFile(filepath.Clean(hashFile))
	s.Require().NoError(err)

	expected, err := hash.File(tarball)
	s.Require().NoError(err)
	s.Equal(expected, string(stored))
}

func (s *IntegrityTestSuite) TestVerifyCacheIntegrity_ValidHash() {
	dir := s.T().TempDir()
	tarball, err := createTestTarball(dir)
	s.Require().NoError(err)

	computed, err := hash.File(tarball)
	s.Require().NoError(err)

	hashFile := filepath.Join(dir, "feature.sha256")
	s.Require().NoError(os.WriteFile(hashFile, []byte(computed), 0o600))

	s.True(verifyCacheIntegrity(dir, "test-feature"))
}

func (s *IntegrityTestSuite) TestVerifyCacheIntegrity_CorruptedHash() {
	dir := s.T().TempDir()
	_, err := createTestTarball(dir)
	s.Require().NoError(err)

	hashFile := filepath.Join(dir, "feature.sha256")
	s.Require().NoError(os.WriteFile(hashFile, []byte("bad-hash"), 0o600))

	s.False(verifyCacheIntegrity(dir, "test-feature"))
}

func (s *IntegrityTestSuite) TestVerifyCacheIntegrity_MissingHashFile() {
	dir := s.T().TempDir()
	_, err := createTestTarball(dir)
	s.Require().NoError(err)

	// No .sha256 file — backward compat: should return true.
	s.True(verifyCacheIntegrity(dir, "test-feature"))
}
