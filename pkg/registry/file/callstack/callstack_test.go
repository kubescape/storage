package callstack

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

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

func TestUnifyIdentifiedCallStacksWithDummyRoots(t *testing.T) {
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
				Frame: types.StackFrame{}, // Empty frame = dummy root
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

	testCases := []struct {
		name      string
		stacks    []types.IdentifiedCallStack
		callID    types.CallID
		wantLen   int
		wantDummy bool
	}{
		{
			name: "Both have dummy roots",
			stacks: []types.IdentifiedCallStack{
				{CallID: "test1", CallStack: createStackWithDummy("1", "1")},
				{CallID: "test1", CallStack: createStackWithDummy("2", "2")},
			},
			callID:    "test1",
			wantLen:   2,
			wantDummy: true,
		},
		{
			name: "First has dummy, second doesn't",
			stacks: []types.IdentifiedCallStack{
				{CallID: "test2", CallStack: createStackWithDummy("1", "1")},
				{CallID: "test2", CallStack: createStackNoDummy("2", "2")},
			},
			callID:    "test2",
			wantLen:   2,
			wantDummy: true,
		},
		{
			name: "First doesn't have dummy, second has",
			stacks: []types.IdentifiedCallStack{
				{CallID: "test3", CallStack: createStackNoDummy("1", "1")},
				{CallID: "test3", CallStack: createStackWithDummy("2", "2")},
			},
			callID:    "test3",
			wantLen:   2,
			wantDummy: true,
		},
		{
			name: "Neither has dummy",
			stacks: []types.IdentifiedCallStack{
				{CallID: "test4", CallStack: createStackNoDummy("1", "1")},
				{CallID: "test4", CallStack: createStackNoDummy("2", "2")},
			},
			callID:    "test4",
			wantLen:   2,
			wantDummy: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := UnifyIdentifiedCallStacks(tc.stacks)

			// Find the result stack with matching CallID
			var found *types.CallStack
			for _, r := range result {
				if r.CallID == tc.callID {
					found = &r.CallStack
					break
				}
			}

			if found == nil {
				t.Fatalf("No result found for CallID %s", tc.callID)
			}

			// Check if root is dummy when expected
			if tc.wantDummy && !isEmptyFrame(found.Root.Frame) {
				t.Error("Root should be dummy node")
			}

			// Check number of children
			if len(found.Root.Children) != tc.wantLen {
				t.Errorf("Want %d children under root, got %d", tc.wantLen, len(found.Root.Children))
			}

			// Verify all original nodes are present
			seen := make(map[string]bool)
			var checkNodes func(types.CallStackNode)
			checkNodes = func(node types.CallStackNode) {
				if !isEmptyFrame(node.Frame) {
					key := node.Frame.FileID + ":" + node.Frame.Lineno
					seen[key] = true
				}
				for _, child := range node.Children {
					checkNodes(child)
				}
			}

			checkNodes(found.Root)

			// Check that we found both original nodes
			for _, stack := range tc.stacks {
				checkNodes(stack.CallStack.Root)
			}
			if len(seen) != tc.wantLen {
				t.Errorf("Want %d unique nodes, got %d", tc.wantLen, len(seen))
			}
		})
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

func TestUnifyIdentifiedCallStacksRealData(t *testing.T) {
	// Test case based on the repeated patterns in the real data
	stacks := []types.IdentifiedCallStack{
		{
			CallID: "b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "645761"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "653231"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "654232"},
											Children: []types.CallStackNode{
												{
													Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "10678645"},
													Children: []types.CallStackNode{
														{
															Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "12583206"},
															Children: []types.CallStackNode{
																{
																	Frame:    types.StackFrame{FileID: "0", Lineno: "4012"},
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
					},
				},
			},
		},
		{
			CallID: "70e9681008bbee682463bf37966e1d9892138d20cd83cdd00ace9eacbf9b72c3",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "645761"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "653231"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "654496"},
											Children: []types.CallStackNode{
												{
													Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "10678645"},
													Children: []types.CallStackNode{
														{
															Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "12583206"},
															Children: []types.CallStackNode{
																{
																	Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "1087561"},
																	Children: []types.CallStackNode{
																		{
																			Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "1087661"},
																			Children: []types.CallStackNode{
																				{
																					Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "560624"},
																					Children: []types.CallStackNode{
																						{
																							Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "563389"},
																							Children: []types.CallStackNode{
																								{
																									Frame:    types.StackFrame{FileID: "0", Lineno: "4012"},
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
		},
		// Adding a duplicate of the first stack to test unification of identical stacks
		{
			CallID: "b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "645761"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "653231"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "654232"},
											Children: []types.CallStackNode{
												{
													Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "10678645"},
													Children: []types.CallStackNode{
														{
															Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "12583206"},
															Children: []types.CallStackNode{
																{
																	Frame:    types.StackFrame{FileID: "0", Lineno: "4012"},
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
					},
				},
			},
		},
	}
	result := UnifyIdentifiedCallStacks(stacks)

	// Should have exactly two CallStacks after unification (one for each unique CallID)
	if len(result) != 2 {
		t.Errorf("Expected 2 unified CallStacks, got %d", len(result))
	}

	// Create a map for easier testing
	resultMap := make(map[types.CallID]types.CallStack)
	for _, stack := range result {
		resultMap[stack.CallID] = stack.CallStack
	}

	// Test the first CallID's stack (b9e3...)
	firstStack, exists := resultMap["b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329"]
	if !exists {
		t.Error("Missing expected CallStack for first CallID")
	} else {
		// Should have one path with exactly 6 levels
		validateCallStackDepth(t, firstStack.Root, 6, "first")

		// Verify specific path
		validatePath(t, firstStack.Root, []types.StackFrame{
			{FileID: "10425069705252389217", Lineno: "645761"},
			{FileID: "10425069705252389217", Lineno: "653231"},
			{FileID: "10425069705252389217", Lineno: "654232"},
			{FileID: "10425069705252389217", Lineno: "10678645"},
			{FileID: "10425069705252389217", Lineno: "12583206"},
			{FileID: "0", Lineno: "4012"},
		})
	}

	// Test the second CallID's stack (70e9...)
	secondStack, exists := resultMap["70e9681008bbee682463bf37966e1d9892138d20cd83cdd00ace9eacbf9b72c3"]
	if !exists {
		t.Error("Missing expected CallStack for second CallID")
	} else {
		// Should have one path with exactly 10 levels
		validateCallStackDepth(t, secondStack.Root, 10, "second")

		// Verify specific path
		validatePath(t, secondStack.Root, []types.StackFrame{
			{FileID: "10425069705252389217", Lineno: "645761"},
			{FileID: "10425069705252389217", Lineno: "653231"},
			{FileID: "10425069705252389217", Lineno: "654496"},
			{FileID: "10425069705252389217", Lineno: "10678645"},
			{FileID: "10425069705252389217", Lineno: "12583206"},
			{FileID: "2918313636494991837", Lineno: "1087561"},
			{FileID: "2918313636494991837", Lineno: "1087661"},
			{FileID: "2918313636494991837", Lineno: "560624"},
			{FileID: "2918313636494991837", Lineno: "563389"},
			{FileID: "0", Lineno: "4012"},
		})
	}
}

// Helper function to validate the depth of a call stack
func validateCallStackDepth(t *testing.T, node types.CallStackNode, expectedDepth int, stackName string) {
	depth := 0
	current := node
	for len(current.Children) > 0 {
		depth++
		current = current.Children[0]
	}
	if depth != expectedDepth {
		t.Errorf("%s stack: Expected depth of %d, got %d", stackName, expectedDepth, depth)
	}
}

// Helper function to validate a specific path in the call stack
func validatePath(t *testing.T, root types.CallStackNode, expectedFrames []types.StackFrame) {
	current := root
	for i, expectedFrame := range expectedFrames {
		if len(current.Children) == 0 {
			t.Errorf("Stack ended prematurely at depth %d", i)
			return
		}
		frame := current.Children[0].Frame
		if frame.FileID != expectedFrame.FileID || frame.Lineno != expectedFrame.Lineno {
			t.Errorf("At depth %d: Expected frame (%s:%s), got (%s:%s)",
				i, expectedFrame.FileID, expectedFrame.Lineno, frame.FileID, frame.Lineno)
		}
		current = current.Children[0]
	}
}

func TestUnifyIdentifiedCallStackSingleCallID(t *testing.T) {
	// Test case with two stacks having the same CallID and different paths
	stacks := []types.IdentifiedCallStack{
		{
			CallID: "b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "645761"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "653231"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "654232"},
											Children: []types.CallStackNode{
												{
													Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "10678645"},
													Children: []types.CallStackNode{
														{
															Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "12583206"},
															Children: []types.CallStackNode{
																{
																	Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "869139"},
																	Children: []types.CallStackNode{
																		{
																			Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "867979"},
																			Children: []types.CallStackNode{
																				{
																					Frame:    types.StackFrame{FileID: "4298936378959959569", Lineno: "390324"},
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
									},
								},
							},
						},
					},
				},
			},
		},
		{
			CallID: "b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329",
			CallStack: types.CallStack{
				Root: types.CallStackNode{
					Children: []types.CallStackNode{
						{
							Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "645761"},
							Children: []types.CallStackNode{
								{
									Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "653231"},
									Children: []types.CallStackNode{
										{
											Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "654232"},
											Children: []types.CallStackNode{
												{
													Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "10678645"},
													Children: []types.CallStackNode{
														{
															Frame: types.StackFrame{FileID: "10425069705252389217", Lineno: "12583206"},
															Children: []types.CallStackNode{
																{
																	Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "869139"},
																	Children: []types.CallStackNode{
																		{
																			Frame: types.StackFrame{FileID: "2918313636494991837", Lineno: "867979"},
																			Children: []types.CallStackNode{
																				{
																					Frame:    types.StackFrame{FileID: "0", Lineno: "4012"},
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
									},
								},
							},
						},
					},
				},
			},
		},
	}

	result := UnifyIdentifiedCallStacks(stacks)

	// Should have exactly one CallStack after unification
	if len(result) != 1 {
		t.Errorf("Expected 1 unified CallStack, got %d", len(result))
		return
	}

	// Verify the unified stack has the correct call ID
	if result[0].CallID != "b9e310c00779300bebd7f9fc616a8a6d74e2b44bfb8e1a1bc206e70014096329" {
		t.Error("Incorrect CallID in unified stack")
		return
	}

	// Navigate to the branching point (after "867979")
	current := result[0].CallStack.Root.Children[0] // Start at first real node
	for i := 0; i < 6; i++ {                        // Navigate through the common path
		if len(current.Children) == 0 {
			t.Errorf("Stack ended prematurely at depth %d", i)
			return
		}
		current = current.Children[0]
	}

	// At branching point (867979 node), verify it has both paths
	if len(current.Children) != 2 {
		t.Errorf("Expected 2 branches after 867979 node, got %d", len(current.Children))
		return
	}

	// Verify both branches exist
	foundBranch1 := false
	foundBranch2 := false
	for _, child := range current.Children {
		if child.Frame.FileID == "4298936378959959569" && child.Frame.Lineno == "390324" {
			foundBranch1 = true
		}
		if child.Frame.FileID == "0" && child.Frame.Lineno == "4012" {
			foundBranch2 = true
		}
	}

	if !foundBranch1 {
		t.Error("Missing branch with 390324")
	}
	if !foundBranch2 {
		t.Error("Missing branch with 4012")
	}
}

func TestUnifyIdentifiedCallStacksComplex2(t *testing.T) {
	testCases := []struct {
		name         string
		stacks       []types.IdentifiedCallStack
		expectedSize int
		validateFunc func(*testing.T, []types.IdentifiedCallStack)
	}{
		{
			name: "Multiple branches at different levels",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "1"}, {"4", "1"}},
				}),
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "2"}, {"4", "2"}},
				}),
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "2"}, {"3", "3"}, {"4", "3"}},
				}),
			},
			expectedSize: 1,
			validateFunc: func(t *testing.T, result []types.IdentifiedCallStack) {
				stack := result[0]
				// Should branch at level 2 (two branches) and level 3 (additional branch)
				current := stack.CallStack.Root.Children[0]
				if len(current.Children) != 2 { // First branch point
					t.Errorf("Expected 2 branches at first level, got %d", len(current.Children))
				}
			},
		},
		{
			name: "Multiple call IDs with shared prefixes",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "1"}},
				}),
				buildCallStack("id2", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "2"}},
				}),
				buildCallStack("id2", []framePath{
					{{"1", "1"}, {"2", "2"}, {"3", "3"}},
				}),
			},
			expectedSize: 2,
			validateFunc: func(t *testing.T, result []types.IdentifiedCallStack) {
				if len(result) != 2 {
					t.Errorf("Expected 2 distinct call IDs, got %d", len(result))
				}
				// Verify each call ID has the correct number of branches
				for _, stack := range result {
					if stack.CallID == "id1" {
						if len(stack.CallStack.Root.Children[0].Children) != 1 {
							t.Error("id1 should have single path")
						}
					}
					if stack.CallID == "id2" {
						if len(stack.CallStack.Root.Children[0].Children) != 2 {
							t.Error("id2 should have two branches")
						}
					}
				}
			},
		},
		{
			name: "Deep branches with reconvergence",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "1"}, {"4", "1"}, {"5", "1"}},
				}),
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "2"}, {"4", "1"}, {"5", "1"}},
				}),
			},
			expectedSize: 1,
			validateFunc: func(t *testing.T, result []types.IdentifiedCallStack) {
				stack := result[0]
				// Verify branch at level 3 and reconvergence at level 4
				current := stack.CallStack.Root.Children[0].Children[0]
				if len(current.Children) != 2 {
					t.Error("Expected branch at level 3")
				}
				// Both branches should converge back to the same frame
				frame1 := current.Children[0].Children[0].Frame
				frame2 := current.Children[1].Children[0].Frame
				if !framesEqual(frame1, frame2) {
					t.Error("Expected paths to reconverge")
				}
			},
		},
		{
			name: "Multiple branches with empty frames",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"", ""}, {"3", "1"}},
				}),
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "2"}, {"3", "1"}},
				}),
			},
			expectedSize: 1,
			validateFunc: func(t *testing.T, result []types.IdentifiedCallStack) {
				// Verify handling of empty frames
				stack := result[0]
				current := stack.CallStack.Root.Children[0]
				if len(current.Children) != 2 {
					t.Error("Expected branch after first frame")
				}
			},
		},
		{
			name: "Cyclic patterns",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "1"}, {"2", "1"}, {"3", "1"}},
				}),
				buildCallStack("id1", []framePath{
					{{"1", "1"}, {"2", "1"}, {"3", "2"}, {"2", "1"}, {"3", "1"}},
				}),
			},
			expectedSize: 1,
			validateFunc: func(t *testing.T, result []types.IdentifiedCallStack) {
				// Verify handling of repeating patterns
				stack := result[0]
				current := stack.CallStack.Root.Children[0].Children[0]
				if len(current.Children) != 2 {
					t.Error("Expected branch at repeated pattern")
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := UnifyIdentifiedCallStacks(tc.stacks)
			if len(result) != tc.expectedSize {
				t.Errorf("Expected %d stacks, got %d", tc.expectedSize, len(result))
			}
			tc.validateFunc(t, result)
		})
	}
}

// Helper type for building test cases
type framePath []types.StackFrame

// Helper function to build a call stack from a series of frames
func buildCallStack(id types.CallID, paths []framePath) types.IdentifiedCallStack {
	cs := types.CallStack{
		Root: types.CallStackNode{
			Children: make([]types.CallStackNode, 0),
		},
	}

	for _, path := range paths {
		current := &cs.Root
		for _, frame := range path {
			node := types.CallStackNode{
				Frame:    types.StackFrame{FileID: frame.FileID, Lineno: frame.Lineno},
				Children: make([]types.CallStackNode, 0),
			}
			current.Children = append(current.Children, node)
			current = &current.Children[len(current.Children)-1]
		}
	}

	return types.IdentifiedCallStack{
		CallID:    id,
		CallStack: cs,
	}
}

func TestUnifyCallStacksEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		stacks   []types.IdentifiedCallStack
		validate func(*testing.T, []types.IdentifiedCallStack)
	}{
		{
			name: "Complex branch divergence",
			stacks: []types.IdentifiedCallStack{
				// First stack
				buildCallStack("test1", []framePath{
					{
						{"1", "1"},
						{"2", "1"},
						{"3", "1"},
						{"4", "1"},
						{"5", "1"},
					},
				}),
				// Second stack with early divergence
				buildCallStack("test1", []framePath{
					{
						{"1", "1"},
						{"2", "2"}, // Diverges here
						{"3", "2"},
						{"4", "2"},
						{"5", "2"},
					},
				}),
				// Third stack that shares part of first path but diverges later
				buildCallStack("test1", []framePath{
					{
						{"1", "1"},
						{"2", "1"},
						{"3", "1"},
						{"4", "3"}, // Diverges later
						{"5", "3"},
					},
				}),
			},
			validate: func(t *testing.T, result []types.IdentifiedCallStack) {
				if len(result) != 1 {
					t.Fatalf("Expected 1 stack, got %d", len(result))
				}

				stack := result[0].CallStack
				root := stack.Root.Children[0] // First real node

				// Verify first level (1,1)
				if !framesEqual(root.Frame, types.StackFrame{FileID: "1", Lineno: "1"}) {
					t.Error("Root should be (1,1)")
				}

				// Should have 2 branches after (1,1): (2,1) and (2,2)
				if len(root.Children) != 2 {
					t.Errorf("Expected 2 children at level 2, got %d", len(root.Children))
					return
				}

				// Find the (2,1) branch
				var branch21 *types.CallStackNode
				for i := range root.Children {
					if framesEqual(root.Children[i].Frame, types.StackFrame{FileID: "2", Lineno: "1"}) {
						branch21 = &root.Children[i]
						break
					}
				}

				if branch21 == nil {
					t.Error("Missing (2,1) branch")
					return
				}

				// The (2,1) branch should split at (4,1) and (4,3)
				found31 := false
				for _, node := range branch21.Children {
					if framesEqual(node.Frame, types.StackFrame{FileID: "3", Lineno: "1"}) {
						found31 = true
						if len(node.Children) != 2 {
							t.Errorf("Expected branch at (3,1) to have 2 children, got %d", len(node.Children))
						}
					}
				}

				if !found31 {
					t.Error("Missing (3,1) node in first branch")
				}

				// Print the entire tree for debugging
				// t.Logf("Tree structure:\n%s", printTree(stack.Root, 0))
			},
		},
		{
			name: "Branch with special frame [0:4012]",
			stacks: []types.IdentifiedCallStack{
				buildCallStack("test2", []framePath{
					{
						{"1", "1"},
						{"2", "1"},
						{"3", "1"},
						{"0", "4012"},
					},
				}),
				buildCallStack("test2", []framePath{
					{
						{"1", "1"},
						{"2", "1"},
						{"3", "1"},
						{"4", "1"},
						{"5", "1"},
					},
				}),
			},
			validate: func(t *testing.T, result []types.IdentifiedCallStack) {
				if len(result) != 1 {
					t.Fatalf("Expected 1 stack, got %d", len(result))
				}

				stack := result[0].CallStack

				// Follow path to [0:4012]
				current := stack.Root.Children[0]
				for i := 0; i < 2; i++ {
					if len(current.Children) == 0 {
						t.Errorf("Path ended too early at depth %d", i)
						return
					}
					current = current.Children[0]
				}

				// At this point we should have two branches
				if len(current.Children) != 2 {
					t.Errorf("Expected 2 branches at divergence point, got %d", len(current.Children))
					return
				}

				// One branch should be [0:4012]
				found4012 := false
				for _, child := range current.Children {
					if framesEqual(child.Frame, types.StackFrame{FileID: "0", Lineno: "4012"}) {
						found4012 = true
						if len(child.Children) != 0 {
							t.Error("[0:4012] should be a leaf node")
						}
					}
				}

				if !found4012 {
					t.Error("Missing [0:4012] branch")
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := UnifyIdentifiedCallStacks(tc.stacks)
			tc.validate(t, result)
		})
	}
}
