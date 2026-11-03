package zed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type URLSuite struct {
	suite.Suite
}

func TestURLSuite(t *testing.T) {
	suite.Run(t, new(URLSuite))
}

func (s *URLSuite) TestCanonicalHost() {
	s.Equal("zed://ssh/ws.devsy/workspaces/ws", buildOpenURL("ws.devsy", "/workspaces/ws"))
}

func (s *URLSuite) TestAliasHostReplacesCanonical() {
	got := buildOpenURL("friendly.devsy", "/workspaces/ws")
	s.Equal("zed://ssh/friendly.devsy/workspaces/ws", got)
	s.NotContains(got, "ws.devsy/")
}

func (s *URLSuite) TestFolderWithoutLeadingSlash() {
	s.Equal(
		"zed://ssh/friendly.devsy/workspaces/ws",
		buildOpenURL("friendly.devsy", "workspaces/ws"),
	)
}
