package kubernetes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

const (
	limitsPrefix   = "limits."
	requestsPrefix = "requests."
)

func parseResources(resourceString string) corev1.ResourceRequirements {
	resourcesSplitted := strings.Split(resourceString, ",")
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}
	for _, resourceName := range resourcesSplitted {
		resourceName = strings.TrimSpace(resourceName)

		var destination corev1.ResourceList
		var strippedResource string
		if after, ok := strings.CutPrefix(resourceName, requestsPrefix); ok {
			destination = requests
			strippedResource = after
		} else if after, ok := strings.CutPrefix(resourceName, limitsPrefix); ok {
			destination = limits
			strippedResource = after
		} else {
			continue
		}

		name, quantity, err := parseResource(strippedResource)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		destination[corev1.ResourceName(name)] = quantity
	}

	return corev1.ResourceRequirements{
		Limits:   limits,
		Requests: requests,
	}
}

func getPodTemplate(manifest string) (*corev1.Pod, error) {
	// check if manifest is inline yaml
	pod := &corev1.Pod{}
	errInline := yaml.Unmarshal([]byte(manifest), pod)
	if errInline == nil {
		return pod, nil
	}

	// check if manifest is path
	p, err := filepath.Abs(manifest)
	if err != nil {
		return nil, fmt.Errorf(
			"parsing pod tempate failed failed: %w (inline) or %w (file)",
			errInline,
			err,
		)
	}
	//nolint:gosec // path is inside the kubeconfig directory
	body, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf(
			"parsing pod tempate failed failed: %w (inline) or %w (file)",
			errInline,
			err,
		)
	}
	err = yaml.Unmarshal(body, pod)
	if err == nil {
		return pod, nil
	}

	return nil, fmt.Errorf(
		"parsing pod tempate failed failed: %w (inline) or %w (file)",
		errInline,
		err,
	)
}

func parseLabels(str string) (map[string]string, error) {
	if str == "" {
		return nil, nil
	}

	labels := strings.Split(str, ",")
	retMap := map[string]string{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		key, value, ok := strings.Cut(label, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label %q, expected format label=value", label)
		}

		retMap[key] = value
	}

	return retMap, nil
}

func parseResource(resourceName string) (string, resource.Quantity, error) {
	name, value, ok := strings.Cut(resourceName, "=")
	if !ok {
		return "", resource.Quantity{}, fmt.Errorf(
			"error parsing resource %s: expected form resource=quantity",
			resourceName,
		)
	}

	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return "", resource.Quantity{}, fmt.Errorf(
			"error parsing resource %s: %w",
			resourceName,
			err,
		)
	}

	return name, quantity, nil
}

func parseSecurityContext(raw string) (*corev1.SecurityContext, error) {
	if raw == "" {
		return nil, nil
	}

	sc := &corev1.SecurityContext{}
	errInline := yaml.UnmarshalStrict([]byte(raw), sc)
	if errInline == nil {
		return sc, nil
	}

	p, err := filepath.Abs(raw)
	if err != nil {
		return nil, fmt.Errorf(
			"parsing security context failed: %w (inline) or %w (file)",
			errInline,
			err,
		)
	}
	// #nosec G304 -- path comes from the operator-controlled AGENT_SECURITY_CONTEXT provider option
	body, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf(
			"parsing security context failed: %w (inline) or %w (file)",
			errInline,
			err,
		)
	}
	if err = yaml.UnmarshalStrict(body, sc); err == nil {
		return sc, nil
	}

	return nil, fmt.Errorf(
		"parsing security context failed: %w (inline) or %w (file)",
		errInline,
		err,
	)
}

func applyBaseFallback(override, base *corev1.SecurityContext) {
	if base == nil {
		return
	}
	if override.Capabilities == nil {
		override.Capabilities = base.Capabilities
	}
	if override.Privileged == nil {
		override.Privileged = base.Privileged
	}
}

func resolveContainerSecurityContext(
	strictSecurity, agentSecurityContext string,
	base *corev1.SecurityContext,
) (*corev1.SecurityContext, error) {
	override, err := parseSecurityContext(agentSecurityContext)
	if err != nil {
		return nil, fmt.Errorf("AGENT_SECURITY_CONTEXT: %w", err)
	}
	if override != nil {
		applyBaseFallback(override, base)
		return override, nil
	}

	if strictSecurity != pkgconfig.BoolTrue {
		return base, nil
	}
	if base == nil {
		return nil, nil
	}
	return &corev1.SecurityContext{
		Capabilities: base.Capabilities,
		Privileged:   base.Privileged,
	}, nil
}

type securityContextOptions struct {
	Capabilities         *corev1.Capabilities
	Privileged           *bool
	StrictSecurity       string
	AgentSecurityContext string
}

func (o securityContextOptions) resolve() (*corev1.SecurityContext, error) {
	base := &corev1.SecurityContext{
		Capabilities: o.Capabilities,
		Privileged:   o.Privileged,
		RunAsUser:    new(int64),
		RunAsGroup:   new(int64),
		RunAsNonRoot: new(bool),
	}
	return resolveContainerSecurityContext(o.StrictSecurity, o.AgentSecurityContext, base)
}
