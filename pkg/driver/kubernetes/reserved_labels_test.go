package kubernetes

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ReservedLabelsSuite struct{ suite.Suite }

func TestReservedLabelsSuite(t *testing.T) { suite.Run(t, new(ReservedLabelsSuite)) }

func (s *ReservedLabelsSuite) TestTemplateAndProviderLabelsRejectedBeforeProvisioning() {
	for _, key := range []string{pkgconfig.K8sCreatedLabel, pkgconfig.K8sWorkspaceUIDLabel, pkgconfig.DockerUserLabel} {
		s.Run(key, func() {
			_, err := getLabels(
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{key: "private"}},
				},
				"",
			)
			s.ErrorContains(err, key)
			_, err = getLabels(&corev1.Pod{}, key+"=private")
			s.ErrorContains(err, key)
			k := &KubernetesDriver{options: &provider.ProviderKubernetesDriverConfig{}}
			k.options.Labels = key + "=private"
			s.ErrorContains(k.preflightTemplate(&driver.RunOptions{}), key)
		})
	}
}

func (s *ReservedLabelsSuite) TestOrdinaryAndGeneratedLabelsPreserved() {
	labels, err := getLabels(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"custom": "template"}}},
		"other=provider",
	)
	s.Require().NoError(err)
	s.Equal(
		map[string]string{
			"custom":                  "template",
			"other":                   "provider",
			pkgconfig.K8sCreatedLabel: "true",
		},
		labels,
	)
}
