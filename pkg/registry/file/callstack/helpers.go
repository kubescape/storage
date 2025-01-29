package callstack

import (
	"fmt"
	"strings"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// PrintIdentifiedCallStack prints the entire identified call stack
func PrintIdentifiedCallStack(ics types.IdentifiedCallStack) {
	fmt.Printf("Call ID: %s\n", ics.CallID)
	fmt.Println("Call Stack:")
	printCallStackNode(ics.CallStack.Root, 0)
}

// printCallStackNode recursively prints each node in the call stack
func printCallStackNode(node types.CallStackNode, depth int) {
	// Create indentation based on depth
	indent := strings.Repeat("  ", depth)

	// Print current frame
	if node.Frame.FileID != "" || node.Frame.Lineno != "" {
		fmt.Printf("%s├── FileID: %s, Line: %s\n", indent, node.Frame.FileID, node.Frame.Lineno)
	} else {
		fmt.Printf("%s├── Root\n", indent)
	}

	// Print children recursively
	for i, child := range node.Children {
		// Use different character for last child
		if i == len(node.Children)-1 {
			fmt.Printf("%s└── Branch %d:\n", indent, i+1)
		} else {
			fmt.Printf("%s├── Branch %d:\n", indent, i+1)
		}
		printCallStackNode(child, depth+1)
	}
}

// PrettyPrintCallStack is an alternative printer that uses arrows to show call flow
func PrettyPrintCallStack(ics types.IdentifiedCallStack) {
	fmt.Printf("Call ID: %s\n", ics.CallID)
	fmt.Println("Call Stack:")
	prettyPrintNode(ics.CallStack.Root, 0)
}

func prettyPrintNode(node types.CallStackNode, depth int) {
	indent := strings.Repeat("    ", depth)

	// Print current frame
	if node.Frame.FileID != "" || node.Frame.Lineno != "" {
		fmt.Printf("%s↳ [%s:%s]\n", indent, node.Frame.FileID, node.Frame.Lineno)
	} else {
		fmt.Printf("%sRoot\n", indent)
	}

	// Print children
	for _, child := range node.Children {
		prettyPrintNode(child, depth+1)
	}
}
