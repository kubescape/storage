package callstack

import (
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// framesEqual checks if two StackFrames are equal
func framesEqual(f1, f2 types.StackFrame) bool {
	return f1.FileID == f2.FileID && f1.Lineno == f2.Lineno
}

// getNodesToProcess returns the nodes that should be processed for unification
func getNodesToProcess(cs types.CallStack) []types.CallStackNode {
	if cs.Root.Frame != (types.StackFrame{}) {
		// If root has a frame, treat the root itself as a node to process
		return []types.CallStackNode{cs.Root}
	}
	// Otherwise process its children
	return cs.Root.Children
}

// createDummyRoot creates a new CallStack with a dummy root node
func createDummyRoot() types.CallStack {
	return types.CallStack{
		Root: types.CallStackNode{
			Children: make([]types.CallStackNode, 0),
			Frame:    types.StackFrame{},
		},
	}
}

func UnifyCallStacks(cs1, cs2 types.CallStack) types.CallStack {
	unified := createDummyRoot()

	// Process nodes from cs1
	for _, node1 := range getNodesToProcess(cs1) {
		subtree := copySubtree(node1)
		unified.Root.Children = append(unified.Root.Children, subtree)
	}

	// Process nodes from cs2
	for _, node2 := range getNodesToProcess(cs2) {
		merged := false
		for i := range unified.Root.Children {
			existingChild := unified.Root.Children[i]
			if framesEqual(node2.Frame, existingChild.Frame) {
				// If frames are equal at this level, try to merge their children
				for _, child2 := range node2.Children {
					childFound := false
					for _, existingGrandChild := range existingChild.Children {
						if framesEqual(child2.Frame, existingGrandChild.Frame) {
							childFound = true
							break
						}
					}
					if !childFound {
						// Add this as a new path under the existing node
						childCopy := copySubtree(child2)
						unified.Root.Children[i].Children = append(unified.Root.Children[i].Children, childCopy)
					}
				}
				merged = true
				break
			}
		}
		if !merged {
			// Add this as a completely new path
			subtree := copySubtree(node2)
			unified.Root.Children = append(unified.Root.Children, subtree)
		}
	}

	return unified
}

func copySubtree(node types.CallStackNode) types.CallStackNode {
	newNode := types.CallStackNode{
		Children: make([]types.CallStackNode, 0),
		Frame:    node.Frame,
	}

	for _, child := range node.Children {
		childCopy := copySubtree(child)
		newNode.Children = append(newNode.Children, childCopy)
	}

	return newNode
}

// UnifyIdentifiedCallStacks takes a list of IdentifiedCallStack and returns a list of unified CallStacks
func UnifyIdentifiedCallStacks(stacks []types.IdentifiedCallStack) []types.IdentifiedCallStack {
	// Group CallStacks by CallID
	stacksByID := make(map[types.CallID][]types.CallStack)
	for _, stack := range stacks {
		stacksByID[stack.CallID] = append(stacksByID[stack.CallID], stack.CallStack)
	}

	// Unify CallStacks for each CallID
	result := make(map[types.CallID]types.CallStack)
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

	// Convert map to slice
	var resultSlice []types.IdentifiedCallStack
	for callID, unified := range result {
		resultSlice = append(resultSlice, types.IdentifiedCallStack{
			CallID:    callID,
			CallStack: unified,
		})
	}

	return resultSlice
}
