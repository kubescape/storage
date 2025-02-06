package callstack

import (
	"strconv"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// Helper function to create a linear call stack of specified depth
func createLinearCallStack(depth int) types.CallStack {
	root := types.CallStackNode{
		Children: make([]types.CallStackNode, 0),
		Frame:    types.StackFrame{},
	}

	current := &root
	for i := 1; i <= depth; i++ {
		newNode := types.CallStackNode{
			Frame: types.StackFrame{
				FileID: strconv.Itoa(i),
				Lineno: strconv.Itoa(i),
			},
			Children: make([]types.CallStackNode, 0),
		}
		current.Children = append(current.Children, newNode)
		current = &current.Children[len(current.Children)-1]
	}

	return types.CallStack{Root: root}
}

// Helper function to create a branching call stack with specified depth and width
func createBranchingCallStack(depth, width int) types.CallStack {
	root := types.CallStackNode{
		Children: make([]types.CallStackNode, 0),
		Frame:    types.StackFrame{},
	}

	var addChildren func(node *types.CallStackNode, currentDepth, maxDepth int)
	addChildren = func(node *types.CallStackNode, currentDepth, maxDepth int) {
		if currentDepth >= maxDepth {
			return
		}

		for i := 0; i < width; i++ {
			child := types.CallStackNode{
				Frame: types.StackFrame{
					FileID: strconv.Itoa(currentDepth + 1),
					Lineno: strconv.Itoa(i + 1),
				},
				Children: make([]types.CallStackNode, 0),
			}
			node.Children = append(node.Children, child)
			addChildren(&node.Children[len(node.Children)-1], currentDepth+1, maxDepth)
		}
	}

	addChildren(&root, 0, depth)
	return types.CallStack{Root: root}
}

// Benchmark unifying two linear call stacks of varying depths
func BenchmarkUnifyLinearCallStacks(b *testing.B) {
	depths := []int{10, 100, 1000}

	for _, depth := range depths {
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			cs1 := createLinearCallStack(depth)
			cs2 := createLinearCallStack(depth)

			stacks := []types.IdentifiedCallStack{
				{CallID: "test", CallStack: cs1},
				{CallID: "test", CallStack: cs2},
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				UnifyIdentifiedCallStacks(stacks)
			}
			b.ReportAllocs()
		})
	}
}

// Benchmark unifying two branching call stacks with varying depths and widths
func BenchmarkUnifyBranchingCallStacks(b *testing.B) {
	scenarios := []struct {
		depth int
		width int
	}{
		{3, 2}, // 8 nodes
		{3, 3}, // 27 nodes
		{4, 2}, // 16 nodes
		{4, 3}, // 81 nodes
	}

	for _, sc := range scenarios {
		name := "depth=" + strconv.Itoa(sc.depth) + "_width=" + strconv.Itoa(sc.width)
		b.Run(name, func(b *testing.B) {
			cs1 := createBranchingCallStack(sc.depth, sc.width)
			cs2 := createBranchingCallStack(sc.depth, sc.width)

			stacks := []types.IdentifiedCallStack{
				{CallID: "test", CallStack: cs1},
				{CallID: "test", CallStack: cs2},
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				UnifyIdentifiedCallStacks(stacks)
			}
			b.ReportAllocs()
		})
	}
}

// Benchmark unifying identified call stacks with varying group sizes
func BenchmarkUnifyIdentifiedCallStacks(b *testing.B) {
	scenarios := []struct {
		numGroups      int
		stacksPerGroup int
		depth          int
		width          int
	}{
		{2, 2, 3, 2}, // 2 groups, 2 stacks each, moderate size
		{5, 3, 3, 2}, // 5 groups, 3 stacks each, moderate size
		{2, 2, 4, 3}, // 2 groups, 2 stacks each, larger size
		{3, 4, 3, 3}, // 3 groups, 4 stacks each, larger size
	}

	for _, sc := range scenarios {
		name := "groups=" + strconv.Itoa(sc.numGroups) +
			"_stacks=" + strconv.Itoa(sc.stacksPerGroup) +
			"_depth=" + strconv.Itoa(sc.depth) +
			"_width=" + strconv.Itoa(sc.width)

		b.Run(name, func(b *testing.B) {
			var stacks []types.IdentifiedCallStack

			// Create test data
			for g := 0; g < sc.numGroups; g++ {
				for s := 0; s < sc.stacksPerGroup; s++ {
					cs := createBranchingCallStack(sc.depth, sc.width)
					stacks = append(stacks, types.IdentifiedCallStack{
						CallID:    types.CallID("group" + strconv.Itoa(g)),
						CallStack: cs,
					})
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				UnifyIdentifiedCallStacks(stacks)
			}
			b.ReportAllocs()
		})
	}
}

// Benchmark comparing frames with varying scenarios
func BenchmarkFramesEqual(b *testing.B) {
	scenarios := []struct {
		name string
		f1   types.StackFrame
		f2   types.StackFrame
	}{
		{
			name: "empty_frames",
			f1:   types.StackFrame{},
			f2:   types.StackFrame{},
		},
		{
			name: "one_empty",
			f1:   types.StackFrame{FileID: "1", Lineno: "1"},
			f2:   types.StackFrame{},
		},
		{
			name: "equal",
			f1:   types.StackFrame{FileID: "1", Lineno: "1"},
			f2:   types.StackFrame{FileID: "1", Lineno: "1"},
		},
		{
			name: "different",
			f1:   types.StackFrame{FileID: "1", Lineno: "1"},
			f2:   types.StackFrame{FileID: "2", Lineno: "2"},
		},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				framesEqual(sc.f1, sc.f2)
			}
			b.ReportAllocs()
		})
	}
}
