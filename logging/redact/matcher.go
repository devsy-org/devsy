package redact

type byteMatcher struct {
	forward *matchNode
	reverse *matchNode
}

type matchNode struct {
	children map[byte]*matchNode
	failure  *matchNode
	depth    int
	terminal int
	longest  int
	proper   int
}

func newByteMatcher(values []string) *byteMatcher {
	return &byteMatcher{forward: newAutomaton(values, false), reverse: newAutomaton(values, true)}
}

func newAutomaton(values []string, reverse bool) *matchNode {
	root := &matchNode{}
	for _, value := range values {
		insertSecret(root, value, reverse)
	}
	var queue []*matchNode
	for _, child := range root.children {
		child.failure = root
		child.updateOutputs()
		queue = append(queue, child)
	}
	for index := 0; index < len(queue); index++ {
		current := queue[index]
		for key, child := range current.children {
			child.failure = transition(root, current.failure, key)
			child.updateOutputs()
			queue = append(queue, child)
		}
	}
	return root
}

func insertSecret(root *matchNode, value string, reverse bool) {
	current := root
	for i := range len(value) {
		index := i
		if reverse {
			index = len(value) - 1 - i
		}
		key := value[index]
		if current.children == nil {
			current.children = make(map[byte]*matchNode)
		}
		next := current.children[key]
		if next == nil {
			next = &matchNode{depth: current.depth + 1}
			current.children[key] = next
		}
		current = next
	}
	current.terminal = len(value)
}

func (n *matchNode) updateOutputs() {
	n.longest = max(n.terminal, n.failure.longest)
	n.proper = n.failure.proper
	if len(n.children) != 0 {
		n.proper = n.depth
	}
}

func transition(root, state *matchNode, value byte) *matchNode {
	for state != root && state.children[value] == nil {
		state = state.failure
	}
	if next := state.children[value]; next != nil {
		return next
	}
	return root
}

func (r *Redactor) matchLengths(value string, cut int) []int {
	if r == nil || r.matcher == nil || cut == 0 {
		return nil
	}
	root := r.matcher.reverse
	state := root
	var lengths []int
	for index := len(value) - 1; index >= 0; index-- {
		state = transition(root, state, value[index])
		if index < cut && state.longest != 0 {
			lengths = recordMatchLength(lengths, index, state.longest)
		}
	}
	return lengths
}

func recordMatchLength(lengths []int, index, length int) []int {
	if lengths == nil {
		lengths = make([]int, index+1)
	}
	lengths[index] = length
	return lengths
}

func (r *Redactor) secretPrefixBoundary(value string) int {
	if r == nil || r.matcher == nil {
		return len(value)
	}
	root := r.matcher.forward
	state := root
	for i := max(0, len(value)-r.maxLength+1); i < len(value); i++ {
		state = transition(root, state, value[i])
	}
	return len(value) - state.proper
}
