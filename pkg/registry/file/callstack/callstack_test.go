package callstack

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func TestUnifyCallStacks(t *testing.T) {
	/*
		Test Case 1: Simple merge of two linear paths
		CallStack1:           CallStack2:          Expected Result:
		   root                  root                   root
		     |                    |                      |
		    1,1                  1,1                    1,1
		     |                    |                      |
		    2,1                  2,2                   /   \
		                                            2,1    2,2
		Where (x,y) represents (FileID, Lineno)
	*/
	cs1 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "1"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	cs2 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	if len(result.Root.Children) != 1 {
		t.Errorf("Expected root to have 1 child, got %d", len(result.Root.Children))
	}

	// First level should have FileID 1, Lineno 1
	firstLevel := result.Root.Children[0]
	if firstLevel.Frame.FileID != "1" || firstLevel.Frame.Lineno != "1" {
		t.Errorf("Expected first level frame to be (1,1), got (%s,%s)",
			firstLevel.Frame.FileID, firstLevel.Frame.Lineno)
	}

	// Should have two children at second level with different Linenos
	if len(firstLevel.Children) != 2 {
		t.Errorf("Expected first level to have 2 children, got %d", len(firstLevel.Children))
	}

	if firstLevel.Children[0].Frame.FileID != "2" || firstLevel.Children[0].Frame.Lineno != "1" {
		t.Errorf("Expected first child frame to be (2,1), got (%s,%s)",
			firstLevel.Children[0].Frame.FileID, firstLevel.Children[0].Frame.Lineno)
	}

	if firstLevel.Children[1].Frame.FileID != "2" || firstLevel.Children[1].Frame.Lineno != "2" {
		t.Errorf("Expected second child frame to be (2,2), got (%s,%s)",
			firstLevel.Children[1].Frame.FileID, firstLevel.Children[1].Frame.Lineno)
	}
}

func TestUnifyCallStacksWithSameDummyRoot(t *testing.T) {
	/*
		Test Case: Multiple paths under the same dummy root
		CallStack1:              CallStack2:             Expected Result:
		   root                     root                      root
		   /   \                   /   \                    /  |  \
		1,1    1,2              1,2    1,3              1,1  1,2  1,3
		 |      |                |      |                |    /\    |
		2,1    2,2              2,3    2,4              2,1 2,2 2,3 2,4

		Notice that under 1,2 both 2,2 and 2,3 are at the same level
	*/
	cs1 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "1"},
							Children: []types.CallStackNode{},
						},
					},
				},
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	cs2 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "3"},
							Children: []types.CallStackNode{},
						},
					},
				},
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "3"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "4"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	if len(result.Root.Children) != 3 {
		t.Errorf("Should have three children under root, got %d", len(result.Root.Children))
	}

	// Find the node with frame 1,2
	var node12 types.CallStackNode
	foundNode12 := false
	for _, child := range result.Root.Children {
		if child.Frame.FileID == "1" && child.Frame.Lineno == "2" {
			node12 = child
			foundNode12 = true
			break
		}
	}

	if !foundNode12 {
		t.Error("Should have node with frame 1,2")
	}

	if len(node12.Children) != 2 {
		t.Errorf("Node 1,2 should have two children at the same level, got %d", len(node12.Children))
	}

	// Verify that both children of 1,2 are different and at the same level
	childrenFrames := make(map[string]bool)
	for _, child := range node12.Children {
		if child.Frame.FileID != "2" {
			t.Errorf("Expected child FileID to be 2, got %s", child.Frame.FileID)
		}
		childrenFrames[child.Frame.Lineno] = true
	}

	if !childrenFrames["2"] {
		t.Error("Should have child 2,2")
	}
	if !childrenFrames["3"] {
		t.Error("Should have child 2,3")
	}
}

func TestUnifyCallStacksWithDuplicateFrames(t *testing.T) {
	/*
		Test Case: Same frame (3,3) appears in different paths
		CallStack1:              CallStack2:             Expected Result:
		   root                     root                      root
		     |                       |                       /   \
		    1,1                    1,2                    1,1    1,2
		     |                       |                      |      |
		    2,1                    2,3                    2,1    2,3
		     |                       |                      |      |
		    3,3                    3,3                    3,3    3,3

		Frame (3,3) appears under different parents (2,1 and 2,3)
		and should remain as separate nodes in their respective paths
	*/
	cs1 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "2", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "3", Lineno: "3"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
	}

	cs2 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "2", Lineno: "3"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "3", Lineno: "3"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	if len(result.Root.Children) != 2 {
		t.Errorf("Should have two children under root (1,1 and 1,2), got %d", len(result.Root.Children))
	}

	// Find nodes 1,1 and 1,2
	var node11, node12 types.CallStackNode
	found11, found12 := false, false
	for _, child := range result.Root.Children {
		if child.Frame.FileID == "1" {
			if child.Frame.Lineno == "1" {
				node11 = child
				found11 = true
			} else if child.Frame.Lineno == "2" {
				node12 = child
				found12 = true
			}
		}
	}

	// Verify path under 1,1
	if !found11 {
		t.Error("Should have node 1,1")
	}
	if len(node11.Children) != 1 {
		t.Errorf("Node 1,1 should have one child, got %d", len(node11.Children))
	}

	node21 := node11.Children[0]
	if node21.Frame.FileID != "2" || node21.Frame.Lineno != "1" {
		t.Errorf("Expected node (2,1), got (%s,%s)", node21.Frame.FileID, node21.Frame.Lineno)
	}

	if len(node21.Children) != 1 {
		t.Errorf("Node 2,1 should have one child, got %d", len(node21.Children))
	}

	node33_1 := node21.Children[0]
	if node33_1.Frame.FileID != "3" || node33_1.Frame.Lineno != "3" {
		t.Errorf("Expected node (3,3), got (%s,%s)", node33_1.Frame.FileID, node33_1.Frame.Lineno)
	}

	// Verify path under 1,2
	if !found12 {
		t.Error("Should have node 1,2")
	}
	if len(node12.Children) != 1 {
		t.Errorf("Node 1,2 should have one child, got %d", len(node12.Children))
	}

	node23 := node12.Children[0]
	if node23.Frame.FileID != "2" || node23.Frame.Lineno != "3" {
		t.Errorf("Expected node (2,3), got (%s,%s)", node23.Frame.FileID, node23.Frame.Lineno)
	}

	if len(node23.Children) != 1 {
		t.Errorf("Node 2,3 should have one child, got %d", len(node23.Children))
	}

	node33_2 := node23.Children[0]
	if node33_2.Frame.FileID != "3" || node33_2.Frame.Lineno != "3" {
		t.Errorf("Expected node (3,3), got (%s,%s)", node33_2.Frame.FileID, node33_2.Frame.Lineno)
	}

	// Note: NotSame assertion is not needed for value types since they're always different instances
}

func TestUnifyCallStacksWithSameParentDifferentChildren(t *testing.T) {
	/*
	   Test Case: Same parent frame (1,1) with different children
	   CallStack1:              CallStack2:             Expected Result:
	      root                     root                      root
	        |                       |                         |
	       1,1                     1,1                      1,1
	        |                       |                      /    \
	       2,1                     2,2                  2,1     2,2
	*/
	cs1 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "1"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	cs2 := types.CallStack{
		Root: types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	if len(result.Root.Children) != 1 {
		t.Errorf("Should have one child under root (1,1), got %d", len(result.Root.Children))
	}

	// Get node 1,1
	node11 := result.Root.Children[0]
	if node11.Frame.FileID != "1" || node11.Frame.Lineno != "1" {
		t.Errorf("Expected node (1,1), got (%s,%s)", node11.Frame.FileID, node11.Frame.Lineno)
	}

	// Node 1,1 should have two children (2,1 and 2,2)
	if len(node11.Children) != 2 {
		t.Errorf("Node 1,1 should have two children, got %d", len(node11.Children))
	}

	// Verify both children exist
	foundNode21 := false
	foundNode22 := false
	for _, child := range node11.Children {
		if child.Frame.FileID != "2" {
			t.Errorf("Expected child FileID to be 2, got %s", child.Frame.FileID)
		}
		if child.Frame.Lineno == "1" {
			foundNode21 = true
		} else if child.Frame.Lineno == "2" {
			foundNode22 = true
		}
	}

	if !foundNode21 {
		t.Error("Should have child node 2,1")
	}
	if !foundNode22 {
		t.Error("Should have child node 2,2")
	}
}

func TestUnifyIdentifiedCallStacks(t *testing.T) {
	/*
	   Test merging multiple CallStacks with same CallID
	   CallStack1 (ID: "test1"):    CallStack2 (ID: "test1"):    CallStack3 (ID: "test2"):
	         root                        root                          root
	          |                           |                             |
	         1,1                         1,1                          2,2
	          |                           |                             |
	         2,1                         2,2                          3,3
	*/
	stacks := []types.IdentifiedCallStack{
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "2", Lineno: "1"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "2", Lineno: "2"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test2",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "3", Lineno: "3"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Should have two CallStacks (one for each CallID)
	if len(result) != 2 {
		t.Errorf("Expected 2 CallStacks, got %d", len(result))
	}

	// Create a map for easier testing
	resultMap := make(map[types.CallID]types.CallStack)
	for _, stack := range result {
		resultMap[stack.CallID] = stack.CallStack
	}

	// Validate "test1" CallStack
	test1Stack, exists := resultMap["test1"]
	if !exists {
		t.Error("Should have test1 CallStack")
	} else {
		if len(test1Stack.Root.Children) != 1 {
			t.Errorf("test1 Root should have 1 child, got %d", len(test1Stack.Root.Children))
		}

		firstLevel := test1Stack.Root.Children[0]
		if firstLevel.Frame.FileID != "1" || firstLevel.Frame.Lineno != "1" {
			t.Errorf("Expected first level frame (1,1), got (%s,%s)",
				firstLevel.Frame.FileID, firstLevel.Frame.Lineno)
		}

		if len(firstLevel.Children) != 2 {
			t.Errorf("First level should have 2 children, got %d", len(firstLevel.Children))
		}
	}

	// Validate "test2" CallStack
	test2Stack, exists := resultMap["test2"]
	if !exists {
		t.Error("Should have test2 CallStack")
	} else {
		if len(test2Stack.Root.Children) != 1 {
			t.Errorf("test2 Root should have 1 child, got %d", len(test2Stack.Root.Children))
		}

		test2FirstLevel := test2Stack.Root.Children[0]
		if test2FirstLevel.Frame.FileID != "2" || test2FirstLevel.Frame.Lineno != "2" {
			t.Errorf("Expected first level frame (2,2), got (%s,%s)",
				test2FirstLevel.Frame.FileID, test2FirstLevel.Frame.Lineno)
		}

		if len(test2FirstLevel.Children) != 1 {
			t.Errorf("test2 first level should have 1 child, got %d", len(test2FirstLevel.Children))
		}

		test2SecondLevel := test2FirstLevel.Children[0]
		if test2SecondLevel.Frame.FileID != "3" || test2SecondLevel.Frame.Lineno != "3" {
			t.Errorf("Expected second level frame (3,3), got (%s,%s)",
				test2SecondLevel.Frame.FileID, test2SecondLevel.Frame.Lineno)
		}
	}
}

func TestUnifyIdentifiedCallStacksComplex(t *testing.T) {
	/*
	   Test Case: Complex merging scenarios
	   CallStack1 (ID: "test1"):    CallStack2 (ID: "test1"):    CallStack3 (ID: "test1"):
	         root                        root                          root
	          |                           |                             |
	         1,1                         1,1                          1,1
	          |                           |                             |
	         2,1                         2,2                          2,3
	          |                           |                             |
	         3,1                         3,2                          3,3
	          |                           |
	         4,1                         4,2

	   Expected Result for "test1":
	         root
	          |
	         1,1
	        /||\
	      /  ||  \
	    2,1 2,2 2,3
	     |   |    |
	    3,1 3,2  3,3
	     |   |
	    4,1 4,2

	   CallStack4 (empty group ID): Should create empty call stack with dummy root
	*/
	stacks := []types.IdentifiedCallStack{
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "2", Lineno: "1"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "3", Lineno: "1"},
											Children: []types.CallStackNode{
												{
													Frame:    types.StackFrame{FileID: "4", Lineno: "1"},
													Children: []types.CallStackNode{},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "2", Lineno: "2"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "3", Lineno: "2"},
											Children: []types.CallStackNode{
												{
													Frame:    types.StackFrame{FileID: "4", Lineno: "2"},
													Children: []types.CallStackNode{},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "2", Lineno: "3"},
									Children: []types.CallStackNode{
										{
											Frame:    types.StackFrame{FileID: "3", Lineno: "3"},
											Children: []types.CallStackNode{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "", // Empty group test
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Create a map for easier testing
	resultMap := make(map[types.CallID]types.CallStack)
	for _, stack := range result {
		resultMap[stack.CallID] = stack.CallStack
	}

	// Test the empty group case
	emptyStack, exists := resultMap[""]
	if !exists {
		t.Error("Should have empty CallID stack")
	}
	if len(emptyStack.Root.Children) != 1 {
		t.Errorf("Empty stack root should have 1 child, got %d", len(emptyStack.Root.Children))
	}

	// Test the complex merged stack
	test1Stack, exists := resultMap["test1"]
	if !exists {
		t.Error("Should have test1 CallStack")
		return
	}

	// Should have one child at root (1,1)
	if len(test1Stack.Root.Children) != 1 {
		t.Errorf("Should have one child at root, got %d", len(test1Stack.Root.Children))
		return
	}

	node11 := test1Stack.Root.Children[0]
	if node11.Frame.FileID != "1" || node11.Frame.Lineno != "1" {
		t.Errorf("Expected node (1,1), got (%s,%s)", node11.Frame.FileID, node11.Frame.Lineno)
	}

	// Should have three children under 1,1 (2,1 2,2 and 2,3)
	if len(node11.Children) != 3 {
		t.Errorf("Node 1,1 should have three children, got %d", len(node11.Children))
		return
	}

	// Verify each path is complete
	for _, node2 := range node11.Children {
		if node2.Frame.FileID != "2" {
			t.Errorf("Expected FileID 2, got %s", node2.Frame.FileID)
			continue
		}

		switch node2.Frame.Lineno {
		case "1":
			validatePath1(t, node2)
		case "2":
			validatePath2(t, node2)
		case "3":
			validatePath3(t, node2)
		default:
			t.Errorf("Unexpected Lineno %s for FileID 2", node2.Frame.Lineno)
		}
	}
}

func validatePath1(t *testing.T, node2 types.CallStackNode) {
	if len(node2.Children) != 1 {
		t.Errorf("Node 2,1 should have one child, got %d", len(node2.Children))
		return
	}

	node31 := node2.Children[0]
	if node31.Frame.FileID != "3" || node31.Frame.Lineno != "1" {
		t.Errorf("Expected node (3,1), got (%s,%s)", node31.Frame.FileID, node31.Frame.Lineno)
		return
	}

	if len(node31.Children) != 1 {
		t.Errorf("Node 3,1 should have one child, got %d", len(node31.Children))
		return
	}

	node41 := node31.Children[0]
	if node41.Frame.FileID != "4" || node41.Frame.Lineno != "1" {
		t.Errorf("Expected node (4,1), got (%s,%s)", node41.Frame.FileID, node41.Frame.Lineno)
	}
}

func validatePath2(t *testing.T, node2 types.CallStackNode) {
	if len(node2.Children) != 1 {
		t.Errorf("Node 2,2 should have one child, got %d", len(node2.Children))
		return
	}

	node32 := node2.Children[0]
	if node32.Frame.FileID != "3" || node32.Frame.Lineno != "2" {
		t.Errorf("Expected node (3,2), got (%s,%s)", node32.Frame.FileID, node32.Frame.Lineno)
		return
	}

	if len(node32.Children) != 1 {
		t.Errorf("Node 3,2 should have one child, got %d", len(node32.Children))
		return
	}

	node42 := node32.Children[0]
	if node42.Frame.FileID != "4" || node42.Frame.Lineno != "2" {
		t.Errorf("Expected node (4,2), got (%s,%s)", node42.Frame.FileID, node42.Frame.Lineno)
	}
}

func validatePath3(t *testing.T, node2 types.CallStackNode) {
	if len(node2.Children) != 1 {
		t.Errorf("Node 2,3 should have one child, got %d", len(node2.Children))
		return
	}

	node33 := node2.Children[0]
	if node33.Frame.FileID != "3" || node33.Frame.Lineno != "3" {
		t.Errorf("Expected node (3,3), got (%s,%s)", node33.Frame.FileID, node33.Frame.Lineno)
		return
	}

	if len(node33.Children) != 0 {
		t.Errorf("Node 3,3 should have no children, got %d", len(node33.Children))
	}
}

func TestUnifyCallStacksWithDummyRoots(t *testing.T) {
	/*
	   Test all combinations of dummy/non-dummy root nodes:
	   Case 1: Both have dummy roots
	   Case 2: First has dummy, second doesn't
	   Case 3: First doesn't have dummy, second has
	   Case 4: Neither has dummy
	*/

	// Helper to create a stack with dummy root
	createStackWithDummy := func(fileID, lineno string) types.CallStack {
		return types.CallStack{
			Root: types.CallStackNode{
				Children: []types.CallStackNode{
					{
						Frame:    types.StackFrame{FileID: fileID, Lineno: lineno},
						Children: []types.CallStackNode{},
					},
				},
			},
		}
	}

	// Helper to create a stack without dummy root
	createStackNoDummy := func(fileID, lineno string) types.CallStack {
		return types.CallStack{
			Root: types.CallStackNode{
				Frame:    types.StackFrame{FileID: fileID, Lineno: lineno},
				Children: []types.CallStackNode{},
			},
		}
	}

	// Case 1: Both have dummy roots
	cs1 := createStackWithDummy("1", "1")
	cs2 := createStackWithDummy("2", "2")
	result := UnifyCallStacks(cs1, cs2)
	if !isEmptyFrame(result.Root.Frame) {
		t.Error("Root should be dummy node")
	}
	if len(result.Root.Children) != 2 {
		t.Errorf("Should have both children under dummy root, got %d", len(result.Root.Children))
	}

	// Case 2: First has dummy, second doesn't
	cs1 = createStackWithDummy("1", "1")
	cs2 = createStackNoDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	if !isEmptyFrame(result.Root.Frame) {
		t.Error("Root should be dummy node")
	}
	if len(result.Root.Children) != 2 {
		t.Errorf("Should have both children under dummy root, got %d", len(result.Root.Children))
	}

	// Case 3: First doesn't have dummy, second has
	cs1 = createStackNoDummy("1", "1")
	cs2 = createStackWithDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	if !isEmptyFrame(result.Root.Frame) {
		t.Error("Root should be dummy node")
	}
	if len(result.Root.Children) != 2 {
		t.Errorf("Should have both children under dummy root, got %d", len(result.Root.Children))
	}

	// Case 4: Neither has dummy
	cs1 = createStackNoDummy("1", "1")
	cs2 = createStackNoDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	if !isEmptyFrame(result.Root.Frame) {
		t.Error("Root should be dummy node")
	}
	if len(result.Root.Children) != 2 {
		t.Errorf("Should have both children under dummy root, got %d", len(result.Root.Children))
	}
}

func TestUnifyIdentifiedCallStacksWithMixedRoots(t *testing.T) {
	/*
	   Test unifying call stacks where some have dummy roots and others don't
	   within the same CallID group
	*/
	stacks := []types.IdentifiedCallStack{
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{ // With dummy root
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame:    types.StackFrame{FileID: "2", Lineno: "1"},
									Children: []types.CallStackNode{},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: types.CallStackNode{ // Without dummy root
					Frame: types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame:    types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{},
						},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Find test1 stack
	var test1Stack types.CallStack
	found := false
	for _, stack := range result {
		if stack.CallID == "test1" {
			test1Stack = stack.CallStack
			found = true
			break
		}
	}

	if !found {
		t.Fatal("Could not find test1 stack in results")
	}

	if !isEmptyFrame(test1Stack.Root.Frame) {
		t.Error("Result should have dummy root")
	}

	if len(test1Stack.Root.Children) != 1 {
		t.Errorf("Should have one child under root, got %d", len(test1Stack.Root.Children))
		return
	}

	firstLevel := test1Stack.Root.Children[0]
	if firstLevel.Frame.FileID != "1" || firstLevel.Frame.Lineno != "1" {
		t.Errorf("Expected first level frame (1,1), got (%s,%s)",
			firstLevel.Frame.FileID, firstLevel.Frame.Lineno)
	}

	if len(firstLevel.Children) != 2 {
		t.Errorf("Should have both 2,1 and 2,2 children, got %d children",
			len(firstLevel.Children))
	}
}

// Helper function to check if a frame is empty (dummy)
func isEmptyFrame(frame types.StackFrame) bool {
	return frame.FileID == "" && frame.Lineno == ""
}

func TestRealWorldCallStackEncoding(t *testing.T) {
	// Create the call stack structure from your example
	callStack := types.IdentifiedCallStack{
		CallID: "2bea65ce108e73407c3970e448009e58c46dad6f2463c1dbf2d23a92ba5ad81c",
		CallStack: types.CallStack{
			Root: types.CallStackNode{
				Frame: types.StackFrame{
					FileID: "10425069705252389217",
					Lineno: "645761",
				},
				Children: []types.CallStackNode{
					{
						Frame: types.StackFrame{
							FileID: "10425069705252389217",
							Lineno: "653231",
						},
						Children: []types.CallStackNode{
							{
								Frame: types.StackFrame{
									FileID: "10425069705252389217",
									Lineno: "654232",
								},
								Children: []types.CallStackNode{
									{
										Frame: types.StackFrame{
											FileID: "10425069705252389217",
											Lineno: "10678645",
										},
										Children: []types.CallStackNode{},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Try to encode
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(callStack)
	if err != nil {
		t.Fatalf("Encoding error: %v", err)
	}

	// Try to decode
	dec := gob.NewDecoder(&buf)
	var decodedCallStack types.IdentifiedCallStack
	err = dec.Decode(&decodedCallStack)
	if err != nil {
		t.Fatalf("Decoding error: %v", err)
	}

	// Verify the decoded structure matches the original
	if decodedCallStack.CallID != callStack.CallID {
		t.Errorf("CallID mismatch: got %v, want %v", decodedCallStack.CallID, callStack.CallID)
	}

	// Additional verification could be added here to compare the full tree structure
	if !verifyCallStacksEqual(t, decodedCallStack.CallStack, callStack.CallStack) {
		t.Error("Decoded call stack does not match original")
	}
}

func TestGobCallStackEncoding(t *testing.T) {
	// Create a deep call stack
	root := types.CallStackNode{
		Children: make([]types.CallStackNode, 0),
		Frame: types.StackFrame{
			FileID: "10425069705252389217",
			Lineno: "645761",
		},
	}

	// Create a very deep stack to trigger the overflow
	currentChildren := &root.Children
	for i := 0; i < 100; i++ { // Large number to trigger stack overflow
		newNode := types.CallStackNode{
			Children: make([]types.CallStackNode, 0),
			Frame: types.StackFrame{
				FileID: fmt.Sprintf("file_%d", i),
				Lineno: fmt.Sprintf("line_%d", i),
			},
		}
		*currentChildren = append(*currentChildren, newNode)
		currentChildren = &(*currentChildren)[len(*currentChildren)-1].Children
	}

	callStack := types.IdentifiedCallStack{
		CallID:    "test_call_id",
		CallStack: types.CallStack{Root: root},
	}

	t.Logf("Total nodes in call stack: %d", countNodes(callStack.CallStack.Root))

	// Try to encode
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(callStack)
	if err != nil {
		t.Fatalf("Encoding error: %v", err)
	}

	// Try to decode
	dec := gob.NewDecoder(&buf)
	var decodedCallStack types.IdentifiedCallStack
	err = dec.Decode(&decodedCallStack)
	if err != nil {
		t.Fatalf("Decoding error: %v", err)
	}

	// Verify structure (basic check)
	if decodedCallStack.CallID != callStack.CallID {
		t.Errorf("CallID mismatch: got %v, want %v", decodedCallStack.CallID, callStack.CallID)
	}

	// Additional verification of the full tree structure
	if !verifyCallStacksEqual(t, decodedCallStack.CallStack, callStack.CallStack) {
		t.Error("Decoded call stack does not match original")
	}
}

// Helper function to count total nodes in a call stack
func countNodes(node types.CallStackNode) int {
	count := 1
	for _, child := range node.Children {
		count += countNodes(child)
	}
	return count
}

// Helper function to verify if two call stacks are equal
func verifyCallStacksEqual(t *testing.T, cs1, cs2 types.CallStack) bool {
	return verifyNodesEqual(t, cs1.Root, cs2.Root)
}

// Helper function to verify if two nodes and their subtrees are equal
func verifyNodesEqual(t *testing.T, node1, node2 types.CallStackNode) bool {
	if node1.Frame.FileID != node2.Frame.FileID || node1.Frame.Lineno != node2.Frame.Lineno {
		t.Errorf("Frame mismatch: got (%s,%s), want (%s,%s)",
			node1.Frame.FileID, node1.Frame.Lineno,
			node2.Frame.FileID, node2.Frame.Lineno)
		return false
	}

	if len(node1.Children) != len(node2.Children) {
		t.Errorf("Children count mismatch: got %d, want %d",
			len(node1.Children), len(node2.Children))
		return false
	}

	for i := range node1.Children {
		if !verifyNodesEqual(t, node1.Children[i], node2.Children[i]) {
			return false
		}
	}

	return true
}
