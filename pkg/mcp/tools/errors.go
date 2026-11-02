package tools

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrorCode is the code of a tool error. The codes and the ToolError shape are
// the ones kubescape mcpserver uses, so agents handle both the same way.
type ErrorCode string

const (
	ErrCodeResourceNotFound ErrorCode = "RESOURCE_NOT_FOUND"
	ErrCodeRBACDenied       ErrorCode = "RBAC_DENIED"
	ErrCodeInvalidArgument  ErrorCode = "INVALID_ARGUMENT"
	ErrCodeTimeout          ErrorCode = "TIMEOUT"
	// ErrCodeResponseTooLarge is the answer to a call whose result is above
	// the endpoint's size cap.
	ErrCodeResponseTooLarge ErrorCode = "RESPONSE_TOO_LARGE"
)

// ToolError is the JSON text of a tool error result.
type ToolError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// ErrorResult is the tool error result with code, message and details.
func ErrorResult(code ErrorCode, message string, details map[string]any) *mcp.CallToolResult {
	return toolError(code, message, details)
}

func toolError(code ErrorCode, message string, details map[string]any) *mcp.CallToolResult {
	b, err := json.Marshal(ToolError{Code: code, Message: message, Details: details})
	if err != nil {
		return mcp.NewToolResultError(message)
	}
	return mcp.NewToolResultError(string(b))
}

// readError turns a read error the agent can act on into a tool error. Other
// errors are returned as they are and reach the agent as a JSON-RPC error.
func readError(err error) (*mcp.CallToolResult, error) {
	switch {
	case apierrors.IsForbidden(err):
		return toolError(ErrCodeRBACDenied, err.Error(), nil), nil
	case apierrors.IsNotFound(err):
		return toolError(ErrCodeResourceNotFound, err.Error(), nil), nil
	case apierrors.IsBadRequest(err), apierrors.IsInvalid(err):
		return toolError(ErrCodeInvalidArgument, err.Error(), nil), nil
	}
	return nil, err
}

// stringArg returns the string argument name, or "" when it is missing or null.
func stringArg(args map[string]any, name string) (string, *mcp.CallToolResult) {
	switch v := args[name].(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		return "", toolError(ErrCodeInvalidArgument, fmt.Sprintf("%s must be a string", name), map[string]any{"argument": name})
	}
}

// requiredStringArg is stringArg for an argument that must be given and not be
// empty.
func requiredStringArg(args map[string]any, name string) (string, *mcp.CallToolResult) {
	s, toolErr := stringArg(args, name)
	if toolErr != nil {
		return "", toolErr
	}
	if s == "" {
		return "", toolError(ErrCodeInvalidArgument, fmt.Sprintf("%s is required", name), map[string]any{"argument": name})
	}
	return s, nil
}

// boolArg returns the boolean argument name, or false when it is missing or
// null.
func boolArg(args map[string]any, name string) (bool, *mcp.CallToolResult) {
	switch v := args[name].(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	default:
		return false, toolError(ErrCodeInvalidArgument, fmt.Sprintf("%s must be a boolean", name), map[string]any{"argument": name})
	}
}

// intArg returns the integer argument name and whether it was given. JSON
// numbers arrive as float64; a null is treated as missing.
func intArg(args map[string]any, name string) (int, bool, *mcp.CallToolResult) {
	switch v := args[name].(type) {
	case nil:
		return 0, false, nil
	case float64:
		if v == float64(int(v)) {
			return int(v), true, nil
		}
	}
	return 0, false, toolError(ErrCodeInvalidArgument, fmt.Sprintf("%s must be an integer", name), map[string]any{"argument": name})
}
