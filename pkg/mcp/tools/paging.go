package tools

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// page is the limit and offset arguments of a tool that pages through the
// entries of one object.
type page struct {
	limit  int
	offset int
}

// parsePage reads the optional limit (1 to maxLimit, defaultLimit when not
// given) and offset (0 or more) arguments.
func parsePage(args map[string]any, defaultLimit, maxLimit int) (page, *mcp.CallToolResult) {
	p := page{limit: defaultLimit}
	limit, set, toolErr := intArg(args, "limit")
	if toolErr != nil {
		return p, toolErr
	}
	if set {
		if limit < 1 || limit > maxLimit {
			return p, toolError(ErrCodeInvalidArgument,
				fmt.Sprintf("limit must be between 1 and %d", maxLimit),
				map[string]any{"argument": "limit"})
		}
		p.limit = limit
	}
	offset, _, toolErr := intArg(args, "offset")
	if toolErr != nil {
		return p, toolErr
	}
	if offset < 0 {
		return p, toolError(ErrCodeInvalidArgument, "offset must not be negative", map[string]any{"argument": "offset"})
	}
	p.offset = offset
	return p, nil
}

// pageInfo tells the caller where a page is in the whole result.
type pageInfo struct {
	Total      int  `json:"total"`
	Returned   int  `json:"returned"`
	Truncated  bool `json:"truncated"`
	NextOffset *int `json:"next_offset,omitempty"`
}

// paginate returns the entries of p and where they are in items. The page is
// never nil, so that it is [] and not null in JSON.
func paginate[T any](items []T, p page) ([]T, pageInfo) {
	info := pageInfo{Total: len(items)}
	out := []T{}
	if p.offset < len(items) {
		end := min(p.offset+p.limit, len(items))
		out = items[p.offset:end]
		if end < len(items) {
			info.Truncated = true
			next := end
			info.NextOffset = &next
		}
	}
	info.Returned = len(out)
	return out, info
}
