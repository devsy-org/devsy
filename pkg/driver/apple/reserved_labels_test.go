package apple

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/suite"
)

type CreatedLabelsSuite struct{ suite.Suite }

const (
	testUserLabel         = "devsy.user"
	testPrivateLabelValue = "private-value"
	testRemoveCreated     = "remove:created-id"
)

func TestCreatedLabelsSuite(t *testing.T) { suite.Run(t, new(CreatedLabelsSuite)) }

func (s *CreatedLabelsSuite) TestAmbiguousCreateOutputNeverStartsOrDeletes() {
	m := &mockClient{createOutput: "private-value\ncreated-id\n"}
	d := &appleDriver{Apple: m}
	err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		WorkspaceID: "ws", ParsedConfig: &config.DevContainerConfig{},
		Options: &driver.RunOptions{Image: testImageRef},
	})
	s.Require().ErrorContains(err, "inspect the runtime for an unstarted container")
	s.NotContains(err.Error(), testPrivateLabelValue)
	s.Equal([]string{testRunCall}, m.calls)
}

func (s *CreatedLabelsSuite) TestCreateInspectsBeforeStarting() {
	for _, tc := range []struct {
		name      string
		labels    map[string]string
		wantCalls []string
		wantKey   string
	}{
		{"match", map[string]string{testUserLabel: execUser, "team": "a"}, []string{testRunCall, "start:created-id"}, ""},
		{
			"override",
			map[string]string{testUserLabel: testPrivateLabelValue},
			[]string{testRunCall, testRemoveCreated},
			testUserLabel,
		},
		{"missing", nil, []string{testRunCall, testRemoveCreated}, testUserLabel},
		{
			"extra",
			map[string]string{testUserLabel: execUser, "sh.devsy.recovery": testPrivateLabelValue},
			[]string{testRunCall, testRemoveCreated},
			"sh.devsy.recovery",
		},
	} {
		s.Run(tc.name, func() {
			m := &mockClient{found: &config.ContainerDetails{
				ID: "created-id", Config: config.ContainerDetailsConfig{Labels: tc.labels},
			}}
			d := &appleDriver{Apple: m}
			err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
				WorkspaceID:  "ws",
				ParsedConfig: &config.DevContainerConfig{},
				Options: &driver.RunOptions{
					Image:  testImageRef,
					Labels: []string{"devsy.user=root"},
				},
			})
			if tc.wantKey != "" {
				s.Require().ErrorContains(err, tc.wantKey)
				s.NotContains(err.Error(), testPrivateLabelValue)
			} else {
				s.NoError(err)
			}
			s.Equal(tc.wantCalls, m.calls)
		})
	}
}
