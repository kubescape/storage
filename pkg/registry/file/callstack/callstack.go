package callstack

import (
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// NOTE: THIS DOESN'T WORK. IT'S WIP.

// UnifyCallStacks takes two CallStacks and returns a unified CallStack
func UnifyCallStacks(cs1, cs2 *types.CallStack) *types.CallStack {
	if cs1 == nil && cs2 == nil {
		return nil
	}
	if cs1 == nil {
		return cs2
	}
	if cs2 == nil {
		return cs1
	}

	// Create a new unified CallStack with a dummy root node
	unified := &types.CallStack{
		Root: &types.CallStackNode{
			Children: make([]*types.CallStackNode, 0),
			IsStart:  false,
			IsEnd:    false,
			Parent:   nil,
			Frame:    nil,
		},
	}

	// Helper function to copy a node and its children
	var copyNode func(node *types.CallStackNode) *types.CallStackNode
	copyNode = func(node *types.CallStackNode) *types.CallStackNode {
		if node == nil {
			return nil
		}
		newNode := &types.CallStackNode{
			Children: make([]*types.CallStackNode, 0, len(node.Children)),
			IsEnd:    node.IsEnd,
			IsStart:  node.IsStart,
			Parent:   nil,
			Frame:    nil,
		}
		if node.Frame != nil {
			newNode.Frame = &types.StackFrame{
				FileID: node.Frame.FileID,
				Lineno: node.Frame.Lineno,
			}
		}
		for _, child := range node.Children {
			childCopy := copyNode(child)
			childCopy.Parent = newNode
			newNode.Children = append(newNode.Children, childCopy)
		}
		return newNode
	}

	// Helper function to merge nodes
	var mergeNodes func(node1, node2, parent *types.CallStackNode)
	mergeNodes = func(node1, node2, parent *types.CallStackNode) {
		if node1 == nil && node2 == nil {
			return
		}

		// Create maps to group children by frame
		childrenMap := make(map[uint64]map[uint64][]*types.CallStackNode)

		// Helper function to add children to the map
		addToMap := func(nodes []*types.CallStackNode) {
			for _, child := range nodes {
				if child.Frame != nil {
					if _, ok := childrenMap[child.Frame.FileID]; !ok {
						childrenMap[child.Frame.FileID] = make(map[uint64][]*types.CallStackNode)
					}
					childrenMap[child.Frame.FileID][child.Frame.Lineno] = append(
						childrenMap[child.Frame.FileID][child.Frame.Lineno],
						child,
					)
				}
			}
		}

		// Add children from both nodes to the map
		if node1 != nil {
			addToMap(node1.Children)
		}
		if node2 != nil {
			addToMap(node2.Children)
		}

		// Merge children with the same frame
		for _, linenoMap := range childrenMap {
			for _, nodes := range linenoMap {
				if len(nodes) == 0 {
					continue
				}

				// Create a new merged node
				mergedNode := copyNode(nodes[0])
				mergedNode.Parent = parent
				parent.Children = append(parent.Children, mergedNode)

				// Recursively merge children of nodes with the same frame
				var childNode1, childNode2 *types.CallStackNode
				if len(nodes) > 0 {
					childNode1 = nodes[0]
				}
				if len(nodes) > 1 {
					childNode2 = nodes[1]
				}
				mergeNodes(childNode1, childNode2, mergedNode)
			}
		}
	}

	// Start merging from the root nodes
	mergeNodes(cs1.Root, cs2.Root, unified.Root)
	return unified
}

// UnifyIdentifiedCallStacks takes a list of IdentifiedCallStack and returns a map of unified CallStacks
func UnifyIdentifiedCallStacks(stacks []types.IdentifiedCallStack) map[types.CallID]*types.CallStack {
	// Group CallStacks by CallID
	stacksByID := make(map[types.CallID][]*types.CallStack)
	for _, stack := range stacks {
		stacksByID[stack.CallID] = append(stacksByID[stack.CallID], &stack.CallStack)
	}

	// Unify CallStacks for each CallID
	result := make(map[types.CallID]*types.CallStack)
	for callID, callStacks := range stacksByID {
		if len(callStacks) == 0 {
			continue
		}

		// Start with the first CallStack
		unified := callStacks[0]

		// Unify with remaining CallStacks
		for i := 1; i < len(callStacks); i++ {
			unified = UnifyCallStacks(unified, callStacks[i])
		}

		result[callID] = unified
	}

	return result
}
