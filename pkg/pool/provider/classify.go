package provider

import (
	"cmp"
	"maps"
	"slices"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

// Orphan is a resource with a missing or invalid session label. Reason is a
// short machine token.
type Orphan struct {
	Resource Resource
	Reason   string
}

// Classify groups resources with an exact pool label by their valid session ID.
// It returns ErrInvalidPool for an invalid pool name. It does not check for
// session files. Each group and the orphans are sorted by kind and name.
// The returned labels do not alias the input.
func Classify(
	pool string,
	resources []Resource,
) (owned map[string][]Resource, orphans []Orphan, err error) {
	if !poolconfig.ValidPoolName(pool) {
		return nil, nil, ErrInvalidPool
	}
	owned = make(map[string][]Resource)
	for _, r := range resources {
		if r.Labels[LabelPool] != pool {
			continue
		}
		r.Labels = maps.Clone(r.Labels)
		id, ok := r.Labels[LabelSession]
		switch {
		case !ok:
			orphans = append(orphans, Orphan{Resource: r, Reason: "session_label_missing"})
		case !session.OwnsID(pool, id):
			orphans = append(orphans, Orphan{Resource: r, Reason: "session_label_invalid"})
		default:
			owned[id] = append(owned[id], r)
		}
	}
	for _, group := range owned {
		slices.SortFunc(group, compareResources)
	}
	slices.SortFunc(orphans, func(a, b Orphan) int {
		return compareResources(a.Resource, b.Resource)
	})
	return owned, orphans, nil
}

func compareResources(a, b Resource) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
}
