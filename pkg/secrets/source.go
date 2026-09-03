package secrets

import "context"

// ResolvedSecret is the runtime value returned by a secret source.
type ResolvedSecret struct {
	Name      string
	Value     string
	Sensitive bool
	Source    string
}

// Source resolves externally or locally owned secret values. Implementations
// must not persist values as a side effect of Get.
type Source interface {
	Get(ctx context.Context, name string) (ResolvedSecret, error)
}
