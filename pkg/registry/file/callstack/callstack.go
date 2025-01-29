package callstack

import (
	"sort"
	"strings"

	"github.com/dghubble/trie"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// framesEqual checks if two StackFrames are equal
func framesEqual(f1, f2 types.StackFrame) bool {
	return f1.FileID == f2.FileID && f1.Lineno == f2.Lineno
}

func isEmptyFrame(frame types.StackFrame) bool {
	return frame.FileID == "" && frame.Lineno == ""
}

// frameKey returns a string key representing a stack frame
func frameKey(frame types.StackFrame) string {
	return frame.FileID + ":" + frame.Lineno
}

// pathKey converts a call stack path to a single string key
func pathKey(nodes []types.CallStackNode) string {
	parts := make([]string, len(nodes))
	for i, node := range nodes {
		parts[i] = frameKey(node.Frame)
	}
	return strings.Join(parts, "/")
}

// getCallStackPaths returns all complete paths in a call stack
func getCallStackPaths(cs types.CallStack) [][]types.CallStackNode {
	var paths [][]types.CallStackNode
	var traverse func(node types.CallStackNode, currentPath []types.CallStackNode)

	traverse = func(node types.CallStackNode, currentPath []types.CallStackNode) {
		path := append([]types.CallStackNode{}, currentPath...)
		path = append(path, node)

		if len(node.Children) == 0 {
			paths = append(paths, path)
			return
		}
		for _, child := range node.Children {
			traverse(child, path)
		}
	}

	if isEmptyFrame(cs.Root.Frame) {
		for _, child := range cs.Root.Children {
			traverse(child, nil)
		}
	} else {
		traverse(cs.Root, nil)
	}

	return paths
}

// buildTrieFromPaths constructs a trie from all paths in the call stacks
func buildTrieFromPaths(paths [][]types.CallStackNode) *trie.PathTrie {
	t := trie.NewPathTrie()
	for _, path := range paths {
		key := pathKey(path)
		t.Put(key, path)
	}
	return t
}

// parsePath converts a path string back to frame sequence
func parsePath(path string) []types.StackFrame {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	frames := make([]types.StackFrame, len(parts))
	for i, part := range parts {
		frameParts := strings.Split(part, ":")
		if len(frameParts) == 2 {
			frames[i] = types.StackFrame{
				FileID: frameParts[0],
				Lineno: frameParts[1],
			}
		}
	}
	return frames
}

// reconstructCallStack builds a CallStack from a trie
func reconstructCallStack(t *trie.PathTrie) types.CallStack {
	var allPaths [][]types.StackFrame
	t.Walk(func(key string, value interface{}) error {
		if value == nil {
			return nil
		}
		frames := parsePath(key)
		if len(frames) > 0 {
			allPaths = append(allPaths, frames)
		}
		return nil
	})

	// Sort paths by length (longest first) then by content
	sort.Slice(allPaths, func(i, j int) bool {
		if len(allPaths[i]) != len(allPaths[j]) {
			return len(allPaths[i]) > len(allPaths[j])
		}
		// If same length, sort by content for consistency
		for k := 0; k < len(allPaths[i]); k++ {
			if allPaths[i][k].FileID != allPaths[j][k].FileID {
				return allPaths[i][k].FileID < allPaths[j][k].FileID
			}
			if allPaths[i][k].Lineno != allPaths[j][k].Lineno {
				return allPaths[i][k].Lineno < allPaths[j][k].Lineno
			}
		}
		return false
	})

	result := types.CallStack{
		Root: types.CallStackNode{
			Children: make([]types.CallStackNode, 0),
			Frame:    types.StackFrame{},
		},
	}

	// Helper function to find or create a node
	getNode := func(parent *types.CallStackNode, frame types.StackFrame) *types.CallStackNode {
		for i := range parent.Children {
			if framesEqual(parent.Children[i].Frame, frame) {
				return &parent.Children[i]
			}
		}
		parent.Children = append(parent.Children, types.CallStackNode{
			Frame:    frame,
			Children: make([]types.CallStackNode, 0),
		})
		return &parent.Children[len(parent.Children)-1]
	}

	// Build tree from sorted paths
	for _, path := range allPaths {
		current := &result.Root
		for _, frame := range path {
			current = getNode(current, frame)
		}
	}

	return result
}

// UnifyIdentifiedCallStacks takes a list of IdentifiedCallStack and returns a list of unified CallStacks
func UnifyIdentifiedCallStacks(stacks []types.IdentifiedCallStack) []types.IdentifiedCallStack {
	stacksByID := make(map[types.CallID][]types.CallStack)
	for _, stack := range stacks {
		stacksByID[stack.CallID] = append(stacksByID[stack.CallID], stack.CallStack)
	}

	var result []types.IdentifiedCallStack
	for id, groupStacks := range stacksByID {
		if len(groupStacks) == 0 {
			continue
		}

		// Collect all paths from all stacks in this group
		var allPaths [][]types.CallStackNode
		for _, cs := range groupStacks {
			paths := getCallStackPaths(cs)
			allPaths = append(allPaths, paths...)
		}

		// Build trie from all paths
		t := buildTrieFromPaths(allPaths)

		// Reconstruct unified call stack from trie
		unified := reconstructCallStack(t)

		result = append(result, types.IdentifiedCallStack{
			CallID:    id,
			CallStack: unified,
		})
	}

	return result
}

// package callstack

// import (
// 	"fmt"

// 	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
// )

// // framesEqual checks if two StackFrames are equal
// func framesEqual(f1, f2 types.StackFrame) bool {
// 	return f1.FileID == f2.FileID && f1.Lineno == f2.Lineno
// }

// func isEmptyFrame(frame types.StackFrame) bool {
// 	return frame.FileID == "" && frame.Lineno == ""
// }

// // getCallStackPaths returns all complete paths in a call stack
// func getCallStackPaths(cs types.CallStack) [][]types.CallStackNode {
// 	var paths [][]types.CallStackNode
// 	var traverse func(node types.CallStackNode, currentPath []types.CallStackNode)

// 	traverse = func(node types.CallStackNode, currentPath []types.CallStackNode) {
// 		path := append([]types.CallStackNode{}, currentPath...)
// 		path = append(path, node)

// 		if len(node.Children) == 0 {
// 			paths = append(paths, path)
// 			return
// 		}
// 		for _, child := range node.Children {
// 			traverse(child, path)
// 		}
// 	}

// 	// Start traversal from each root node
// 	if isEmptyFrame(cs.Root.Frame) {
// 		for _, child := range cs.Root.Children {
// 			traverse(child, nil)
// 		}
// 	} else {
// 		traverse(cs.Root, nil)
// 	}

// 	return paths
// }

// // copyNode creates a deep copy of a CallStackNode
// func copyNode(node types.CallStackNode) types.CallStackNode {
// 	copy := types.CallStackNode{
// 		Frame:    node.Frame,
// 		Children: make([]types.CallStackNode, 0, len(node.Children)),
// 	}
// 	for _, child := range node.Children {
// 		copy.Children = append(copy.Children, copyNode(child))
// 	}
// 	return copy
// }

// // mergeNodes merges two nodes and their children
// func mergeNodes(n1, n2 *types.CallStackNode) types.CallStackNode {
// 	// Create new merged node with same frame as n1
// 	merged := types.CallStackNode{
// 		Frame:    n1.Frame,
// 		Children: make([]types.CallStackNode, 0),
// 	}

// 	// Map to hold children by FileID:Lineno
// 	childMap := make(map[string]*types.CallStackNode)

// 	// Add all children from n1
// 	for i := range n1.Children {
// 		child := copyNode(n1.Children[i])
// 		key := fmt.Sprintf("%s:%s", child.Frame.FileID, child.Frame.Lineno)
// 		childMap[key] = &child
// 	}

// 	// Merge or add children from n2
// 	for i := range n2.Children {
// 		child := n2.Children[i]
// 		key := fmt.Sprintf("%s:%s", child.Frame.FileID, child.Frame.Lineno)

// 		if existing, ok := childMap[key]; ok {
// 			// Merge with existing child
// 			mergedChild := mergeNodes(existing, &child)
// 			childMap[key] = &mergedChild
// 		} else {
// 			// Add new child
// 			newChild := copyNode(child)
// 			childMap[key] = &newChild
// 		}
// 	}

// 	// Convert map back to slice
// 	for _, child := range childMap {
// 		merged.Children = append(merged.Children, *child)
// 	}

// 	return merged
// }

// // UnifyIdentifiedCallStacks takes a list of IdentifiedCallStack and returns a list of unified CallStacks
// func UnifyIdentifiedCallStacks(stacks []types.IdentifiedCallStack) []types.IdentifiedCallStack {
// 	stacksByID := make(map[types.CallID][]types.CallStack)
// 	for _, stack := range stacks {
// 		stacksByID[stack.CallID] = append(stacksByID[stack.CallID], stack.CallStack)
// 	}

// 	var result []types.IdentifiedCallStack
// 	for id, groupStacks := range stacksByID {
// 		if len(groupStacks) == 0 {
// 			continue
// 		}

// 		// Start with the first stack
// 		unified := groupStacks[0]

// 		// Merge with remaining stacks
// 		for i := 1; i < len(groupStacks); i++ {
// 			// Create unified root node
// 			root := types.CallStackNode{
// 				Frame:    types.StackFrame{},
// 				Children: make([]types.CallStackNode, 0),
// 			}

// 			// Get root nodes to merge
// 			nodes1 := unified.Root.Children
// 			nodes2 := groupStacks[i].Root.Children

// 			// Map to track merged nodes
// 			nodeMap := make(map[string]*types.CallStackNode)

// 			// Add all nodes from unified stack
// 			for i := range nodes1 {
// 				node := copyNode(nodes1[i])
// 				key := fmt.Sprintf("%s:%s", node.Frame.FileID, node.Frame.Lineno)
// 				nodeMap[key] = &node
// 			}

// 			// Merge with nodes from current stack
// 			for i := range nodes2 {
// 				node := nodes2[i]
// 				key := fmt.Sprintf("%s:%s", node.Frame.FileID, node.Frame.Lineno)

// 				if existing, ok := nodeMap[key]; ok {
// 					// Merge with existing node
// 					mergedNode := mergeNodes(existing, &node)
// 					nodeMap[key] = &mergedNode
// 				} else {
// 					// Add new node
// 					newNode := copyNode(node)
// 					nodeMap[key] = &newNode
// 				}
// 			}

// 			// Build final root children list
// 			for _, node := range nodeMap {
// 				root.Children = append(root.Children, *node)
// 			}

// 			unified = types.CallStack{Root: root}
// 		}

// 		result = append(result, types.IdentifiedCallStack{
// 			CallID:    id,
// 			CallStack: unified,
// 		})
// 	}

// 	return result
// }
