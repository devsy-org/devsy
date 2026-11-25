package session

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type IDSuite struct{ suite.Suite }

func TestIDSuite(t *testing.T) { suite.Run(t, new(IDSuite)) }

type fixedReader struct{ b byte }

func (r fixedReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

type shortReader struct{ done bool }

func (r *shortReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, []byte{1, 2, 3}), nil
}

type source interface{ Read([]byte) (int, error) }

func useSource(t testing.TB, src source) {
	old := randomSource
	randomSource = src
	t.Cleanup(func() { randomSource = old })
}

func (s *IDSuite) TestFormatAndUniqueness() {
	shape := regexp.MustCompile("^p-team-a-[a-z2-7]{10}$")
	seen := map[string]bool{}
	for range 5000 {
		id, err := NewID("team-a")
		s.Require().NoError(err)
		s.Regexp(shape, id)
		s.True(ValidID(id))
		s.False(seen[id], "repeated ID %s", id)
		seen[id] = true
	}
}

func (s *IDSuite) TestSuffixComesFromTheRandomSourceAndIsTruncated() {
	useSource(s.T(), fixedReader{0x00})
	id, err := NewID("pool")
	s.Require().NoError(err)
	s.Equal("p-pool-aaaaaaaaaa", id)
	useSource(s.T(), fixedReader{0xff})
	id, err = NewID("pool")
	s.Require().NoError(err)
	s.Equal("p-pool-7777777777", id)
}

func (s *IDSuite) TestFailingOrShortSourceReturnsNoID() {
	for name, src := range map[string]source{"error": errReader{}, "short": &shortReader{}} {
		useSource(s.T(), src)
		id, err := NewID("pool")
		s.Require().Error(err, name)
		s.Empty(id, name)
	}
}

func (s *IDSuite) TestInvalidPoolNamesAreRefused() {
	for _, pool := range []string{"", "Pool", "a_b", "-a", "a-", "a/b", "..", strings.Repeat("a", 33)} {
		id, err := NewID(pool)
		s.Require().ErrorIs(err, errInvalidPool, pool)
		s.Empty(id)
	}
	id, err := NewID(strings.Repeat("a", 32))
	s.Require().NoError(err)
	s.Len(id, 45)
}

func (s *IDSuite) TestValidIDRefusesAnythingThatIsNotAnID() {
	for _, id := range []string{
		"", "p-pool-aaaaaaaaa", "p-pool-aaaaaaaaaaa", "p-pool-AAAAAAAAAA", "p-pool-aaaaaaaaa1",
		"p--aaaaaaaaaa", "p-pool-aaaaaaaaaa\n", "../p-pool-aaaaaaaaaa", "p-pool/x-aaaaaaaaaa",
		"p-pool-aaaaaaaaaa.json", "x-pool-aaaaaaaaaa", "p-" + strings.Repeat("a", 40) + "-aaaaaaaaaa",
	} {
		s.False(ValidID(id), "%q", id)
	}
	s.True(ValidID("p-pool-aaaaaaaaaa"))
}
