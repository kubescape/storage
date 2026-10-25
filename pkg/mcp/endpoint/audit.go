package endpoint

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/mcp/tools"
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
	if r.Allowed {
		decision = "allowed"
	}
	if r.Reason != "" {
		decision += " (" + r.Reason + ")"
	}
	return r.Verb + " " + r.Resource + " in " + target + ": " + decision
}

type auditRecord struct {
	mu    sync.Mutex
	reads []read
}

type auditKey struct{}

func recordRead(ctx context.Context, r read) {
	if rec, ok := ctx.Value(auditKey{}).(*auditRecord); ok {
		rec.mu.Lock()
		rec.reads = append(rec.reads, r)
		rec.mu.Unlock()
	}
}

// audit logs one line per tool call: who called which tool, what it read and
// whether that was allowed, and how it ended. kube-apiserver's audit log has
// the create mcpservers/mcp request itself; this line adds what the call read.
func audit(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rec := &auditRecord{}
		start := time.Now()
		res, err := next(context.WithValue(ctx, auditKey{}, rec), req)

		var userName string
		var groups []string
		if u, ok := genericapirequest.UserFrom(ctx); ok {
			userName, groups = u.GetName(), u.GetGroups()
		}
		rec.mu.Lock()
		reads := rec.reads
		rec.mu.Unlock()
		logger.L().Info("mcp tool call",
			helpers.String("user", userName),
			helpers.Interface("groups", groups),
			helpers.String("tool", req.Params.Name),
			helpers.String("namespace", genericapirequest.NamespaceValue(ctx)),
			helpers.Interface("reads", reads),
			helpers.String("duration", time.Since(start).String()),
			helpers.Int("responseBytes", resultBytes(res)),
			helpers.String("result", resultCode(res, err)))
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
