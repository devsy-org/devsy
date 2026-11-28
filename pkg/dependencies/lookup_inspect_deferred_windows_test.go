package dependencies

import "context"

func (s *lookupModelSuite) TestDefaultInspectionIsDeferred() {
	environment := s.environment(nil)
	path := s.file(testDocker, 0o755)

	candidate, err := environment.observe(context.Background(), path, LookupPATH)

	s.Require().NoError(err)
	s.Equal(FileDeferred, candidate.State)
	s.Equal(LookupInspectionDeferred, candidate.ReasonCode)
	s.Equal(path, candidate.LexicalPath)
	s.Equal(LookupPATH, candidate.Origin)
	s.False(candidate.Executed)
	s.Nil(candidate.Identity)
	s.NotEmpty(candidate.Platform)
}

func (s *lookupModelSuite) TestSearchDefersWithTheDefaultInspector() {
	environment := s.environment(map[string]string{envPath: s.root})

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.Equal(LookupInspectionDeferred, result.ReasonCode)
}
