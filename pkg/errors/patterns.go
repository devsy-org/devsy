package errors

import (
	"regexp"
	"strings"
)

// pattern is one row of the fingerprint table. Exactly one of Substring or
// Regex is set. Walked in declaration order; first match wins.
type pattern struct {
	Substring string
	Regex     *regexp.Regexp
	Code      Code
	Message   string
	Hint      string
	DocURL    string
}

func (p pattern) matches(s string) bool {
	if p.Regex != nil {
		return p.Regex.MatchString(s)
	}
	return strings.Contains(s, p.Substring)
}

// re is a small helper that panics at init time on a bad pattern.
func re(expr string) *regexp.Regexp {
	return regexp.MustCompile(expr)
}

// patterns is the canonical fingerprint table. ORDER MATTERS — more specific
// patterns appear before more generic ones.
//
// Each row's Code is part of the IPC contract; do not rename existing codes.
var patterns = []pattern{
	{
		Substring: "failed to get shared config profile",
		Code:      CodeAWSProfileMissing,
		Message:   "AWS credentials are not configured.",
		Hint:      "Set AWS_PROFILE to an existing profile or create ~/.aws/credentials.",
		DocURL:    "https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html",
	},
	{
		Regex:   re(`InvalidClientTokenId|SignatureDoesNotMatch`),
		Code:    CodeAWSCredsInvalid,
		Message: "AWS credentials are invalid or expired.",
		Hint:    "Refresh your AWS credentials (e.g. `aws sso login`) and try again.",
		DocURL:  "https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html",
	},
	{
		Regex:   re(`MissingRegion|could not find region`),
		Code:    CodeAWSRegionMissing,
		Message: "AWS region is not set.",
		Hint:    "Set AWS_REGION or configure a default region with `aws configure`.",
		DocURL:  "https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html",
	},
	{
		Regex:   re(`permission denied.*docker\.sock`),
		Code:    CodeDockerPermDenied,
		Message: "Your user cannot access the Docker socket.",
		Hint:    "Add your user to the `docker` group, then log out and back in.",
		DocURL:  "https://docs.docker.com/engine/install/linux-postinstall/",
	},
	{
		Substring: "Cannot connect to the Docker daemon",
		Code:      CodeDockerNotRunning,
		Message:   "Docker is not running.",
		Hint:      "Start Docker Desktop or run `sudo systemctl start docker`.",
		DocURL:    "https://docs.docker.com/config/daemon/start/",
	},
	{
		Regex:   re(`stat .*\.kube/config: no such file`),
		Code:    CodeKubeConfigMissing,
		Message: "Kubernetes config not found.",
		Hint:    "Set KUBECONFIG to an existing kubeconfig file or place one at ~/.kube/config.",
		DocURL:  "https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/",
	},
	{
		Regex:   re(`connection refused.*:6443|dial tcp.*:6443`),
		Code:    CodeKubeUnreachable,
		Message: "Cannot reach the Kubernetes API server.",
		Hint:    "Check that your cluster is running and that your kubeconfig points to the right endpoint.",
		DocURL:  "https://kubernetes.io/docs/tasks/access-application-cluster/access-cluster/",
	},
	{
		Substring: "podman.sock: connect: no such file",
		Code:      CodePodmanSocket,
		Message:   "Podman socket is unavailable.",
		Hint:      "Start the Podman service with `systemctl --user start podman.socket`.",
		DocURL:    "https://docs.podman.io/en/latest/markdown/podman-system-service.1.html",
	},
}
