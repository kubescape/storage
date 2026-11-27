package endpoint

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// read is one access check a tool call made.
type read struct {
	Verb      string `json:"verb"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	// Pending is true while the check has not returned. A check still pending
	// when the call's line is logged gets a line of its own when it returns.
	Pending bool `json:"pending,omitempty"`
}

// String is how a read shows in the text log, e.g.
// "list vulnerabilitymanifests in kubescape: allowed".
func (r read) String() string {
	target := r.Namespace
	if r.Name != "" {
		target += "/" + r.Name
	}
	if target == "" {
		target = "all namespaces"
	}
	decision := "denied"
	switch {
	case r.Pending:
		decision = "pending"
	case r.Allowed:
		decision = "allowed"
	}
	if r.Reason != "" {
		decision += " (" + r.Reason + ")"
	}
	return r.Verb + " " + r.Resource + " in " + target + ": " + decision
}

type auditRecord struct {
	mu     sync.Mutex
	reads  []read
	logged bool // the call's line is out; later outcomes are logged on their own
	user   string
	tool   string
}

type auditKey struct{}

// auditLog writes an audit line. Tests replace it to read the lines.
var auditLog = func(msg string, details ...helpers.IDetails) { logger.L().Info(msg, details...) }

// startRead records an access check that has started, as pending, and returns
// the function that records its outcome. The tool's goroutine can outlive the
// call when the deadline fires (see limit), so the outcome can come after the
// call's line was logged; it is then logged on its own line, so that every
// check and its decision are in the log.
func startRead(ctx context.Context, r read) func(read) {
	rec, ok := ctx.Value(auditKey{}).(*auditRecord)
	if !ok {
		return func(read) {}
	}
	r.Pending = true
	rec.mu.Lock()
	i := len(rec.reads)
	rec.reads = append(rec.reads, r)
	rec.mu.Unlock()
	return func(r read) {
		r.Pending = false
		rec.mu.Lock()
		rec.reads[i] = r
		late := rec.logged
		rec.mu.Unlock()
		if late {
			auditLog("mcp tool call: access check finished after the call ended",
				helpers.String("user", rec.user),
				helpers.String("tool", rec.tool),
				helpers.Interface("reads", []read{r}))
		}
	}
}

// audit logs one line per tool call: who called which tool, what it read and
// whether that was allowed, and how it ended. kube-apiserver's audit log has
// the create mcpservers/mcp request itself; this line adds what the call read.
func audit(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var userName string
		var groups []string
		if u, ok := genericapirequest.UserFrom(ctx); ok {
			userName, groups = u.GetName(), u.GetGroups()
		}
		rec := &auditRecord{user: userName, tool: req.Params.Name}
		start := time.Now()
		res, err := next(context.WithValue(ctx, auditKey{}, rec), req)

		rec.mu.Lock()
		reads := slices.Clone(rec.reads)
		rec.logged = true
		rec.mu.Unlock()
		duration, size, result := time.Since(start), resultBytes(res), resultCode(res, err)
		metrics.ObserveMCPToolCall(req.Params.Name, result, duration, size)
		auditLog("mcp tool call",
			helpers.String("user", userName),
			helpers.Interface("groups", groups),
			helpers.String("tool", req.Params.Name),
			helpers.String("namespace", genericapirequest.NamespaceValue(ctx)),
			helpers.Interface("reads", reads),
			helpers.String("duration", duration.String()),
			helpers.Int("responseBytes", size),
			helpers.String("result", result))
		return res, err
	}
}

func resultBytes(res *mcp.CallToolResult) int {
	n := 0
	if res != nil {
		for _, c := range res.Content {
			if t, ok := c.(mcp.TextContent); ok {
				n += len(t.Text)
			}
		}
	}
	return n
}

// resultCode is "ok", the tool error's code, or "error" for a JSON-RPC error.
func resultCode(res *mcp.CallToolResult, err error) string {
	switch {
	case err != nil || res == nil:
		return "error"
	case !res.IsError:
		return "ok"
	}
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			var te tools.ToolError
			if json.Unmarshal([]byte(t.Text), &te) == nil && te.Code != "" {
				return string(te.Code)
			}
		}
	}
	return "error"
}
