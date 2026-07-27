package microsandbox

import (
	"context"
	"io"
	"time"
)

type sandboxSpec struct {
	Image       string
	Entrypoint  []string
	Memory      uint32
	CPUs        uint8
	Env         map[string]string
	Labels      map[string]string
	Ephemeral   bool
	IdleTimeout time.Duration
	Mounts      []volumeMount
	MaxMemory   uint32
	MaxCPUs     uint8
	BlockEgress bool
}

type volumeMount struct {
	Target   string
	Source   string
	Volume   string
	Tmpfs    bool
	ReadOnly bool
}

type sandboxInfo struct {
	Name      string
	Running   bool
	CreatedAt time.Time
	Labels    map[string]string
}

type execRequest struct {
	Command string
	Argv    []string
	User    string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// sandboxClient abstracts the microsandbox runtime so the driver can be tested
// against a fake. Lifecycle ordering (e.g. stop before remove) is the driver's
// job, not the client's.
type sandboxClient interface {
	EnsureInstalled(ctx context.Context) error
	EnsureImage(ctx context.Context, image string) error
	Create(ctx context.Context, name string, spec sandboxSpec) error
	Find(ctx context.Context, name string) (*sandboxInfo, error)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	Exec(ctx context.Context, name string, req execRequest) error
	Logs(ctx context.Context, name string, w io.Writer) error
}
