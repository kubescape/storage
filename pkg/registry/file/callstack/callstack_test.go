package callstack

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
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
	cs1 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "1"}},
					},
				},
			},
		},
	}

	cs2 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "2"}},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	assert.NotNil(t, result)
	assert.NotNil(t, result.Root)
	assert.Equal(t, 1, len(result.Root.Children))

	// First level should have FileID 1, Lineno 1
	firstLevel := result.Root.Children[0]
	assert.Equal(t, "1", firstLevel.Frame.FileID)
	assert.Equal(t, "1", firstLevel.Frame.Lineno)

	// Should have two children at second level with different Linenos
	assert.Equal(t, 2, len(firstLevel.Children))
	assert.Equal(t, "2", firstLevel.Children[0].Frame.FileID)
	assert.Equal(t, "1", firstLevel.Children[0].Frame.Lineno)
	assert.Equal(t, "2", firstLevel.Children[1].Frame.FileID)
	assert.Equal(t, "2", firstLevel.Children[1].Frame.Lineno)
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
	cs1 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "1"}},
					},
				},
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "2"}},
					},
				},
			},
		},
	}

	cs2 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "3"}},
					},
				},
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "3"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "4"}},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	assert.NotNil(t, result)
	assert.NotNil(t, result.Root)
	assert.Equal(t, 3, len(result.Root.Children), "Should have three children under root")

	// Find the node with frame 1,2
	var node12 types.CallStackNode
	for _, child := range result.Root.Children {
		if child.Frame.FileID == "1" && child.Frame.Lineno == "2" {
			node12 = child
			break
		}
	}

	assert.NotNil(t, node12, "Should have node with frame 1,2")
	assert.Equal(t, 2, len(node12.Children), "Node 1,2 should have two children at the same level")

	// Verify that both children of 1,2 are different and at the same level
	childrenFrames := make(map[string]bool)
	for _, child := range node12.Children {
		assert.Equal(t, "2", child.Frame.FileID)
		childrenFrames[child.Frame.Lineno] = true
	}
	assert.True(t, childrenFrames["2"], "Should have child 2,2")
	assert.True(t, childrenFrames["3"], "Should have child 2,3")
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
	cs1 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "2", Lineno: "1"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "3", Lineno: "3"}},
							},
						},
					},
				},
			},
		},
	}

	cs2 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "2"},
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "2", Lineno: "3"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "3", Lineno: "3"}},
							},
						},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	assert.NotNil(t, result)
	assert.NotNil(t, result.Root)
	assert.Equal(t, 2, len(result.Root.Children), "Should have two children under root (1,1 and 1,2)")

	// Find nodes 1,1 and 1,2
	var node11, node12 types.CallStackNode
	for _, child := range result.Root.Children {
		if child.Frame.FileID == "1" {
			if child.Frame.Lineno == "1" {
				node11 = child
			} else if child.Frame.Lineno == "2" {
				node12 = child
			}
		}
	}

	// Verify path under 1,1
	assert.NotNil(t, node11, "Should have node 1,1")
	assert.Equal(t, 1, len(node11.Children), "Node 1,1 should have one child")
	node21 := node11.Children[0]
	assert.Equal(t, "2", node21.Frame.FileID)
	assert.Equal(t, "1", node21.Frame.Lineno)
	assert.Equal(t, 1, len(node21.Children), "Node 2,1 should have one child")
	node33_1 := node21.Children[0]
	assert.Equal(t, "3", node33_1.Frame.FileID)
	assert.Equal(t, "3", node33_1.Frame.Lineno)

	// Verify path under 1,2
	assert.NotNil(t, node12, "Should have node 1,2")
	assert.Equal(t, 1, len(node12.Children), "Node 1,2 should have one child")
	node23 := node12.Children[0]
	assert.Equal(t, "2", node23.Frame.FileID)
	assert.Equal(t, "3", node23.Frame.Lineno)
	assert.Equal(t, 1, len(node23.Children), "Node 2,3 should have one child")
	node33_2 := node23.Children[0]
	assert.Equal(t, "3", node33_2.Frame.FileID)
	assert.Equal(t, "3", node33_2.Frame.Lineno)

	// Verify the two 3,3 nodes are different instances
	assert.NotSame(t, node33_1, node33_2, "The two 3,3 nodes should be different instances")
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
	cs1 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "1"}},
					},
				},
			},
		},
	}

	cs2 := &types.CallStack{
		Root: &types.CallStackNode{
			Children: []types.CallStackNode{
				{
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "2"}},
					},
				},
			},
		},
	}

	result := UnifyCallStacks(cs1, cs2)

	// Test structure validation
	assert.NotNil(t, result)
	assert.NotNil(t, result.Root)
	assert.Equal(t, 1, len(result.Root.Children), "Should have one child under root (1,1)")

	// Get node 1,1
	node11 := result.Root.Children[0]
	assert.Equal(t, "1", node11.Frame.FileID)
	assert.Equal(t, "1", node11.Frame.Lineno)

	// Node 1,1 should have two children (2,1 and 2,2)
	assert.Equal(t, 2, len(node11.Children), "Node 1,1 should have two children")

	// Verify both children exist
	foundNode21 := false
	foundNode22 := false
	for _, child := range node11.Children {
		assert.Equal(t, "2", child.Frame.FileID)
		if child.Frame.Lineno == "1" {
			foundNode21 = true
		} else if child.Frame.Lineno == "2" {
			foundNode22 = true
		}
	}
	assert.True(t, foundNode21, "Should have child node 2,1")
	assert.True(t, foundNode22, "Should have child node 2,2")
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
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "2", Lineno: "1"}},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "2", Lineno: "2"}},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test2",
			CallStack: types.CallStack{
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "2", Lineno: "2"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "3", Lineno: "3"}},
							},
						},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Should have two CallStacks (one for each CallID)
	assert.Equal(t, 2, len(result))

	// Create a map for easier testing
	resultMap := make(map[types.CallID]types.CallStack)
	for _, stack := range result {
		resultMap[stack.CallID] = stack.CallStack
	}

	// Validate "test1" CallStack
	test1Stack, exists := resultMap["test1"]
	assert.True(t, exists, "Should have test1 CallStack")
	assert.Equal(t, 1, len(test1Stack.Root.Children))
	firstLevel := test1Stack.Root.Children[0]
	assert.Equal(t, "1", firstLevel.Frame.FileID)
	assert.Equal(t, "1", firstLevel.Frame.Lineno)
	assert.Equal(t, 2, len(firstLevel.Children))

	// Validate "test2" CallStack
	test2Stack, exists := resultMap["test2"]
	assert.True(t, exists, "Should have test2 CallStack")
	assert.Equal(t, 1, len(test2Stack.Root.Children))
	test2FirstLevel := test2Stack.Root.Children[0]
	assert.Equal(t, "2", test2FirstLevel.Frame.FileID)
	assert.Equal(t, "2", test2FirstLevel.Frame.Lineno)
	assert.Equal(t, 1, len(test2FirstLevel.Children))
	test2SecondLevel := test2FirstLevel.Children[0]
	assert.Equal(t, "3", test2SecondLevel.Frame.FileID)
	assert.Equal(t, "3", test2SecondLevel.Frame.Lineno)
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
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: &types.StackFrame{FileID: "2", Lineno: "1"},
									Children: []types.CallStackNode{
										{
											Frame: &types.StackFrame{FileID: "3", Lineno: "1"},
											Children: []types.CallStackNode{
												{Frame: &types.StackFrame{FileID: "4", Lineno: "1"}},
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
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: &types.StackFrame{FileID: "2", Lineno: "2"},
									Children: []types.CallStackNode{
										{
											Frame: &types.StackFrame{FileID: "3", Lineno: "2"},
											Children: []types.CallStackNode{
												{Frame: &types.StackFrame{FileID: "4", Lineno: "2"}},
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
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{
									Frame: &types.StackFrame{FileID: "2", Lineno: "3"},
									Children: []types.CallStackNode{
										{Frame: &types.StackFrame{FileID: "3", Lineno: "3"}},
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
				Root: &types.CallStackNode{
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "1", Lineno: "1"}},
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
	assert.True(t, exists, "Should have empty CallID stack")
	assert.NotNil(t, emptyStack.Root)
	assert.Equal(t, 1, len(emptyStack.Root.Children))

	// Test the complex merged stack
	test1Stack, exists := resultMap["test1"]
	assert.True(t, exists, "Should have test1 CallStack")
	assert.NotNil(t, test1Stack.Root)

	// Should have one child at root (1,1)
	assert.Equal(t, 1, len(test1Stack.Root.Children))
	node11 := test1Stack.Root.Children[0]
	assert.Equal(t, "1", node11.Frame.FileID)
	assert.Equal(t, "1", node11.Frame.Lineno)

	// Should have three children under 1,1 (2,1 2,2 and 2,3)
	assert.Equal(t, 3, len(node11.Children))

	// Verify each path is complete
	for _, node2 := range node11.Children {
		assert.Equal(t, "2", node2.Frame.FileID)
		if node2.Frame.Lineno == "1" {
			assert.Equal(t, 1, len(node2.Children))
			node31 := node2.Children[0]
			assert.Equal(t, "3", node31.Frame.FileID)
			assert.Equal(t, "1", node31.Frame.Lineno)
			assert.Equal(t, 1, len(node31.Children))
			node41 := node31.Children[0]
			assert.Equal(t, "4", node41.Frame.FileID)
			assert.Equal(t, "1", node41.Frame.Lineno)
		} else if node2.Frame.Lineno == "2" {
			assert.Equal(t, 1, len(node2.Children))
			node32 := node2.Children[0]
			assert.Equal(t, "3", node32.Frame.FileID)
			assert.Equal(t, "2", node32.Frame.Lineno)
			assert.Equal(t, 1, len(node32.Children))
			node42 := node32.Children[0]
			assert.Equal(t, "4", node42.Frame.FileID)
			assert.Equal(t, "2", node42.Frame.Lineno)
		} else if node2.Frame.Lineno == "3" {
			assert.Equal(t, 1, len(node2.Children))
			node33 := node2.Children[0]
			assert.Equal(t, "3", node33.Frame.FileID)
			assert.Equal(t, "3", node33.Frame.Lineno)
			assert.Equal(t, 0, len(node33.Children))
		}
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
	createStackWithDummy := func(fileID, lineno string) *types.CallStack {
		return &types.CallStack{
			Root: &types.CallStackNode{
				Children: []types.CallStackNode{
					{
						Frame: &types.StackFrame{FileID: fileID, Lineno: lineno},
					},
				},
			},
		}
	}

	// Helper to create a stack without dummy root
	createStackNoDummy := func(fileID, lineno string) *types.CallStack {
		return &types.CallStack{
			Root: &types.CallStackNode{
				Frame: &types.StackFrame{FileID: fileID, Lineno: lineno},
			},
		}
	}

	// Case 1: Both have dummy roots
	cs1 := createStackWithDummy("1", "1")
	cs2 := createStackWithDummy("2", "2")
	result := UnifyCallStacks(cs1, cs2)
	assert.Nil(t, result.Root.Frame, "Root should be dummy node")
	assert.Equal(t, 2, len(result.Root.Children), "Should have both children under dummy root")

	// Case 2: First has dummy, second doesn't
	cs1 = createStackWithDummy("1", "1")
	cs2 = createStackNoDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	assert.Nil(t, result.Root.Frame, "Root should be dummy node")
	assert.Equal(t, 2, len(result.Root.Children), "Should have both children under dummy root")

	// Case 3: First doesn't have dummy, second has
	cs1 = createStackNoDummy("1", "1")
	cs2 = createStackWithDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	assert.Nil(t, result.Root.Frame, "Root should be dummy node")
	assert.Equal(t, 2, len(result.Root.Children), "Should have both children under dummy root")

	// Case 4: Neither has dummy
	cs1 = createStackNoDummy("1", "1")
	cs2 = createStackNoDummy("2", "2")
	result = UnifyCallStacks(cs1, cs2)
	assert.Nil(t, result.Root.Frame, "Root should be dummy node")
	assert.Equal(t, 2, len(result.Root.Children), "Should have both children under dummy root")
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
				Root: &types.CallStackNode{ // With dummy root
					Children: []types.CallStackNode{
						{
							Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
							Children: []types.CallStackNode{
								{Frame: &types.StackFrame{FileID: "2", Lineno: "1"}},
							},
						},
					},
				},
			},
		},
		{
			CallID: "test1",
			CallStack: types.CallStack{
				Root: &types.CallStackNode{ // Without dummy root
					Frame: &types.StackFrame{FileID: "1", Lineno: "1"},
					Children: []types.CallStackNode{
						{Frame: &types.StackFrame{FileID: "2", Lineno: "2"}},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Find test1 stack
	var test1Stack *types.CallStack
	for _, stack := range result {
		if stack.CallID == "test1" {
			test1Copy := stack.CallStack
			test1Stack = &test1Copy
			break
		}
	}

	assert.NotNil(t, test1Stack)
	assert.Nil(t, test1Stack.Root.Frame, "Result should have dummy root")
	assert.Equal(t, 1, len(test1Stack.Root.Children), "Should have one child under root")
	firstLevel := test1Stack.Root.Children[0]
	assert.Equal(t, "1", firstLevel.Frame.FileID)
	assert.Equal(t, "1", firstLevel.Frame.Lineno)
	assert.Equal(t, 2, len(firstLevel.Children), "Should have both 2,1 and 2,2 children")
}

func TestRealWorldCallStackEncoding(t *testing.T) {
	// Create the call stack structure from your example
	callStack := &types.IdentifiedCallStack{
		CallID: "2bea65ce108e73407c3970e448009e58c46dad6f2463c1dbf2d23a92ba5ad81c",
		CallStack: types.CallStack{
			Root: &types.CallStackNode{
				Frame: &types.StackFrame{
					FileID: "10425069705252389217",
					Lineno: "645761",
				},
				Children: []types.CallStackNode{
					{
						Frame: &types.StackFrame{
							FileID: "10425069705252389217",
							Lineno: "653231",
						},
						Children: []types.CallStackNode{
							{
								Frame: &types.StackFrame{
									FileID: "10425069705252389217",
									Lineno: "654232",
								},
								Children: []types.CallStackNode{
									{
										Frame: &types.StackFrame{
											FileID: "10425069705252389217",
											Lineno: "10678645",
										},
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
		t.Logf("Encoding error: %v", err)
		t.Fail()
	}

	// Try to decode
	dec := gob.NewDecoder(&buf)
	var decodedCallStack types.IdentifiedCallStack
	err = dec.Decode(&decodedCallStack)
	if err != nil {
		t.Logf("Decoding error: %v", err)
		t.Fail()
	}

	// Verify the decoded structure matches the original
	if decodedCallStack.CallID != callStack.CallID {
		t.Errorf("CallID mismatch: got %v, want %v", decodedCallStack.CallID, callStack.CallID)
	}
}

func TestGobCallStackEncoding(t *testing.T) {
	// Create a deep call stack
	root := types.CallStackNode{
		Children: make([]types.CallStackNode, 0),
		Frame: &types.StackFrame{
			FileID: "10425069705252389217",
			Lineno: "645761",
		},
	}

	// Create a very deep stack to trigger the overflow
	currentNode := root
	for i := 0; i < 100; i++ { // Large number to trigger stack overflow
		newNode := types.CallStackNode{
			Children: make([]types.CallStackNode, 0),
			Frame: &types.StackFrame{
				FileID: fmt.Sprintf("file_%d", i),
				Lineno: fmt.Sprintf("line_%d", i),
			},
		}
		currentNode.Children = append(currentNode.Children, newNode)
		currentNode = newNode
	}

	callStack := &types.IdentifiedCallStack{
		CallID: "test_call_id",
		CallStack: types.CallStack{
			Root: &root,
		},
	}

	t.Logf("Total nodes in call stack: %d", countNodes(callStack.CallStack.Root))

	// Try to encode
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(callStack)
	if err != nil {
		t.Logf("Encoding error: %v", err)
		t.Fail()
	}

	// Try to decode
	dec := gob.NewDecoder(&buf)
	var decodedCallStack types.IdentifiedCallStack
	err = dec.Decode(&decodedCallStack)
	if err != nil {
		t.Logf("Decoding error: %v", err)
		t.Fail()
	}

	// Verify structure (basic check)
	if decodedCallStack.CallID != callStack.CallID {
		t.Errorf("CallID mismatch: got %v, want %v", decodedCallStack.CallID, callStack.CallID)
	}
}

// Helper function to count total nodes in a call stack
func countNodes(node *types.CallStackNode) int {
	if node == nil {
		return 0
	}
	count := 1
	for _, child := range node.Children {
		count += countNodes(&child)
	}
	return count
}
