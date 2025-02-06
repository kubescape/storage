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

// isEmptyFrame checks if a StackFrame is empty
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

		// Build trie directly from all paths
		t := trie.NewPathTrie()
		for _, cs := range groupStacks {
			paths := getCallStackPaths(cs)
			for _, path := range paths {
				key := pathKey(path)
				t.Put(key, path)
			}
		}

		// Reconstruct unified call stack from trie
		unified := reconstructCallStack(t)

		result = append(result, types.IdentifiedCallStack{
			CallID:    id,
			CallStack: unified,
		})
	}

	return result
}
