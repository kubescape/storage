package endpoint

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// limit bounds every tool call: its work gets timeout, and a result whose text
// is above maxBytes is replaced by a tool error. The deadline is on the tool's
// work, not on the HTTP request, so the agent always gets an answer: on a
// deadline on the request, mcp-go drops the result and the agent gets an empty
// 200 (designs-and-proposals storage-mcp-endpoint.md, section 7).
func limit(timeout time.Duration, maxBytes int) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			type answer struct {
				res *mcp.CallToolResult
				err error
			}
			// The tool runs on its own goroutine so that a read that does not
			// watch its context still cannot hold the answer past the deadline.
			done := make(chan answer, 1)
			go func() {
				res, err := next(ctx, req)
				done <- answer{res, err}
			}()

			var a answer
			select {
			case a = <-done:
			case <-ctx.Done():
				if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					// The caller went away; there is nobody to answer.
					return nil, ctx.Err()
				}
				return timeoutResult(timeout), nil
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && (a.err != nil || a.res == nil || a.res.IsError) {
				// The tool gave up because of the deadline.
				return timeoutResult(timeout), nil
			}
			if a.err == nil {
				if n := resultBytes(a.res); n > maxBytes {
					return tools.ErrorResult(tools.ErrCodeResponseTooLarge,
						fmt.Sprintf("the result is %d bytes, more than the %d this endpoint returns; ask for less with the tool's paging (limit/offset, continue) or filters", n, maxBytes),
						map[string]any{"bytes": n, "max_bytes": maxBytes}), nil
				}
			}
			return a.res, a.err
		}
	}
}

func timeoutResult(timeout time.Duration) *mcp.CallToolResult {
	return tools.ErrorResult(tools.ErrCodeTimeout,
		fmt.Sprintf("the call did not finish within %s; ask for less with the tool's paging or filters", timeout),
		map[string]any{"timeout": timeout.String()})
}
