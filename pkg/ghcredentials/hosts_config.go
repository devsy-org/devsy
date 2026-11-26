package ghcredentials

import "gopkg.in/yaml.v3"

func decodeHosts(data []byte) (*yaml.Node, error) {
	hosts, err := decode(data)
	if err != nil {
		return nil, err
	}
	if !validAuthMapping(hosts, func(_ string, host *yaml.Node) bool { return validHost(host) }) {
		return nil, errInvalidConfig
	}
	return hosts, nil
}

func validAuthMapping(n *yaml.Node, validValue func(string, *yaml.Node) bool) bool {
	if nullable(n) {
		return true
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	seen := make(map[string]bool)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if key.Kind != yaml.ScalarNode || seen[key.Value] ||
			!validValue(key.Value, n.Content[i+1]) {
			return false
		}
		seen[key.Value] = true
	}
	return true
}

func validHost(n *yaml.Node) bool {
	return validAuthMapping(n, func(key string, value *yaml.Node) bool {
		switch key {
		case activeUserKey, oauthTokenKey:
			return validAuthScalar(value)
		case "users":
			return validAuthMapping(
				value,
				func(_ string, user *yaml.Node) bool { return validUser(user) },
			)
		default:
			return true
		}
	})
}

func validUser(n *yaml.Node) bool {
	if n != nil && n.Kind == yaml.ScalarNode && n.Value == "" {
		return true
	}
	return validAuthMapping(n, func(key string, value *yaml.Node) bool {
		return key != oauthTokenKey || validAuthScalar(value)
	})
}

func validAuthScalar(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode
}

func nullable(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}
