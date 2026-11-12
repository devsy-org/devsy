package config

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ReservedLabelsSuite struct{ suite.Suite }

func TestReservedLabelsSuite(t *testing.T) { suite.Run(t, new(ReservedLabelsSuite)) }

func (s *ReservedLabelsSuite) TestRejectsEveryReservedKeyWithoutDisclosingValues() {
	keys := []string{
		"devsy.user",
		"sh.devsy.recovery",
		"devsy.overlay.structure",
		"devsy.compose.contract",
		"devsy.compose.invocation",
		"devsy.wslg.snapshot",
		"devsy.sh/metadata-creation-config",
		"devsy.sh/user",
		"devsy.sh/microsandbox-workspace-mount",
		"devsy.sh/microsandbox-workspace-user",
		"sh.devsy.managed",
		"sh.devsy.workspace-id",
		"sh.devsy.resource",
		"sh.devsy.volume-role",
		"sh.devsy.seeded",
		"devsy.sh/created",
		"devsy.sh/workspace-uid",
		"devsy.sh/managed",
		"devsy.sh/resource",
		"devsy.sh/volume-role",
	}
	s.Len(reservedLabels, len(keys))
	for _, key := range keys {
		s.Run(key, func() {
			for _, value := range []string{"", "root", "fixture-private-value=with=equals"} {
				err := ValidateUserLabels([]string{key + "=" + value})
				s.Require().ErrorContains(err, key)
				s.Contains(err.Error(), "reserved by Devsy")
				if value != "" {
					s.NotContains(err.Error(), value)
				}
			}
		})
	}
}

func (s *ReservedLabelsSuite) TestPreservesOrdinaryLabelsAndSupportedMetadata() {
	labels := []string{
		"custom=value=with=equals",
		"dev.containers.id=custom-id",
		"devcontainer.metadata=[]",
		"devsy.user.extra=ordinary",
	}
	original := append([]string(nil), labels...)
	s.NoError(ValidateUserLabels(labels))
	s.Equal(original, labels)
	s.NoError(ValidateUserLabels(nil))
}

func (s *ReservedLabelsSuite) TestCreatedLabels() {
	for _, tc := range []struct {
		name     string
		expected []string
		actual   map[string]string
		key      string
	}{
		{"match", []string{"devsy.user=root"}, map[string]string{"devsy.user": "root"}, ""},
		{"ordinary", nil, map[string]string{"team": "a"}, ""},
		{"override", []string{"devsy.user=root"}, map[string]string{"devsy.user": "private-value"}, "devsy.user"},
		{"missing", []string{"devsy.user=private-value"}, nil, "devsy.user"},
		{"extra", nil, map[string]string{"sh.devsy.recovery": "private-value"}, "sh.devsy.recovery"},
	} {
		s.Run(tc.name, func() {
			err := ValidateCreatedLabels(tc.expected, tc.actual)
			if tc.key == "" {
				s.NoError(err)
				return
			}
			s.Require().ErrorContains(err, tc.key)
			s.NotContains(err.Error(), "private-value")
		})
	}
}
