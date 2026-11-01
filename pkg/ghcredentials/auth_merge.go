package ghcredentials

import (
	"errors"
	"sort"

	"gopkg.in/yaml.v3"
)

type authenticationConflictError struct{}

func (*authenticationConflictError) Error() string {
	return "GitHub CLI authentication changed concurrently; newer identity preserved"
}

var errAuthConflict = &authenticationConflictError{}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		out.Content[i] = cloneNode(child)
	}
	return &out
}

func authentication(h *yaml.Node) *yaml.Node {
	if h == nil {
		return nil
	}
	a := mapping()
	for _, key := range []string{activeUserKey, oauthTokenKey} {
		put(a, key, cloneNode(get(h, key)))
	}
	if users := get(h, "users"); users != nil {
		registrations := mapping()
		for i := 0; i+1 < len(users.Content); i += 2 {
			u := mapping()
			put(u, oauthTokenKey, cloneNode(get(users.Content[i+1], oauthTokenKey)))
			put(registrations, users.Content[i].Value, u)
		}
		if len(registrations.Content) > 0 {
			put(a, "users", registrations)
		}
	}
	if len(a.Content) == 0 {
		return nil
	}
	return a
}

func preferences(h *yaml.Node) *yaml.Node {
	if h == nil {
		return nil
	}
	p := cloneNode(h)
	put(p, activeUserKey, nil)
	put(p, oauthTokenKey, nil)
	if users := get(p, "users"); users != nil {
		for _, key := range sortedKeys(users) {
			u := get(users, key)
			put(u, oauthTokenKey, nil)
			if u == nil || len(u.Content) == 0 {
				put(users, key, nil)
			}
		}
		if len(users.Content) == 0 {
			put(p, "users", nil)
		}
	}
	if len(p.Content) == 0 {
		return nil
	}
	return p
}

func sortedKeys(nodes ...*yaml.Node) []string {
	keys := mappingKeys(nodes...)
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func mergeHosts(before, desired, latest []byte) ([]byte, error) {
	b, err := decodeHosts(before)
	if err != nil {
		return nil, err
	}
	d, err := decodeHosts(desired)
	if err != nil {
		return nil, err
	}
	l, err := decodeHosts(latest)
	if err != nil {
		return nil, err
	}
	if equal(b, d) {
		return latest, nil
	}
	result := mapping()
	var conflict error
	for _, host := range sortedKeys(b, d, l) {
		node, err := mergeHost(get(b, host), get(d, host), get(l, host))
		conflict = errors.Join(conflict, err)
		put(result, host, node)
	}
	if equal(result, l) {
		return latest, conflict
	}
	out, err := yaml.Marshal(result)
	return out, errors.Join(conflict, err)
}

func mergeHost(base, want, current *yaml.Node) (*yaml.Node, error) {
	if equal(base, want) {
		return cloneNode(current), nil
	}
	chosen, err := selectAuthentication(
		authentication(base),
		authentication(want),
		authentication(current),
	)
	p := merge(preferences(base), preferences(want), preferences(current))
	if p == nil {
		p = mapping()
	}
	applyAuthentication(p, chosen)
	if len(p.Content) == 0 {
		return nil, err
	}
	return p, err
}

func selectAuthentication(base, want, current *yaml.Node) (*yaml.Node, error) {
	switch {
	case equal(base, want), equal(current, want):
		return current, nil
	case equal(current, base):
		return want, nil
	default:
		return current, errAuthConflict
	}
}

func applyAuthentication(p, chosen *yaml.Node) {
	removeUnregisteredPreferences(p, get(chosen, "users"))
	for _, key := range []string{activeUserKey, oauthTokenKey} {
		put(p, key, cloneNode(get(chosen, key)))
	}
	users := get(chosen, "users")
	for _, user := range sortedKeys(users) {
		u := ensure(ensure(p, "users"), user)
		put(u, oauthTokenKey, cloneNode(get(get(users, user), oauthTokenKey)))
	}
}

func removeUnregisteredPreferences(p, registrations *yaml.Node) {
	users := get(p, "users")
	if users == nil {
		return
	}
	for _, user := range sortedKeys(users) {
		if get(registrations, user) == nil {
			put(users, user, nil)
		}
	}
	if len(users.Content) == 0 {
		put(p, "users", nil)
	}
}
