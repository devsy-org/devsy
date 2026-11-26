package secrets

// An immutable byte trie matches the longest known secret without scanning
// every retained value at each output position. Byte keys preserve binary data.
type secretNode struct {
	children map[byte]*secretNode
	length   int
}

func newSecretMatcher(values []string) *secretNode {
	root := &secretNode{}
	for _, value := range values {
		node := root
		for index := range len(value) {
			if node.children == nil {
				node.children = make(map[byte]*secretNode)
			}
			child := node.children[value[index]]
			if child == nil {
				child = &secretNode{}
				node.children[value[index]] = child
			}
			node = child
		}
		node.length = len(value)
	}
	return root
}
