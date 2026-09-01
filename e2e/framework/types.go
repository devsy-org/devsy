package framework

// PodList is a list of Pods.
type PodList struct {
	Items []Pod `json:"items"`
}

type Pod struct {
	Spec PodSpec `json:"spec"`
}

type PodSpec struct {
	Containers []PodContainer `json:"containers,omitempty"`
	HostUsers  *bool          `json:"hostUsers,omitempty"`
}

type PodContainer struct {
	Image           string           `json:"image,omitempty"`
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`
}

type SecurityContext struct {
	RunAsUser    *int64 `json:"runAsUser,omitempty"`
	RunAsGroup   *int64 `json:"runAsGroup,omitempty"`
	RunAsNonRoot *bool  `json:"runAsNonRoot,omitempty"`
}
