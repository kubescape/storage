package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/go-logger/helpers"

	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
)

func callLimited(t *testing.T, ctx context.Context, timeout time.Duration, maxBytes int, tool server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	t.Helper()
	return limit(timeout, maxBytes)(tool)(ctx, mcp.CallToolRequest{})
}

func limitErrorOf(t *testing.T, res *mcp.CallToolResult) tools.ToolError {
	t.Helper()
	require.NotNil(t, res)
	require.True(t, res.IsError)
	require.Len(t, res.Content, 1)
	var te tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &te))
	return te
}

func TestLimitPassesAnswersThrough(t *testing.T) {
	want := mcp.NewToolResultText(`{"ok":true}`)
	res, err := callLimited(t, context.Background(), time.Second, 100, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return want, nil
	})
	require.NoError(t, err)
	assert.Same(t, want, res)

	toolErr := tools.ErrorResult(tools.ErrCodeRBACDenied, "denied", nil)
	res, err = callLimited(t, context.Background(), time.Second, 100, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return toolErr, nil
	})
	require.NoError(t, err)
	assert.Same(t, toolErr, res, "a tool error before the deadline is kept")

	boom := errors.New("boom")
	_, err = callLimited(t, context.Background(), time.Second, 100, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, boom
	})
	assert.ErrorIs(t, err, boom)
}

func TestLimitDeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool server.ToolHandlerFunc
	}{
		{"tool ignores its context", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			time.Sleep(time.Second)
			return mcp.NewToolResultText("late"), nil
		}},
		{"tool returns the context error", func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}},
		{"tool turns the context error into a tool error", func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return tools.ErrorResult(tools.ErrCodeResourceNotFound, ctx.Err().Error(), nil), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			res, err := callLimited(t, context.Background(), 50*time.Millisecond, 100, tc.tool)
			require.NoError(t, err)
			assert.Less(t, time.Since(start), 500*time.Millisecond, "the answer does not wait for the tool")
			te := limitErrorOf(t, res)
			assert.Equal(t, tools.ErrCodeTimeout, te.Code)
			assert.Equal(t, "50ms", te.Details["timeout"])
		})
	}
}

func TestLimitResponseSize(t *testing.T) {
	big := mcp.NewToolResultText(strings.Repeat("x", 101))
	res, err := callLimited(t, context.Background(), time.Second, 100, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return big, nil
	})
	require.NoError(t, err)
	te := limitErrorOf(t, res)
	assert.Equal(t, tools.ErrCodeResponseTooLarge, te.Code)
	assert.EqualValues(t, 101, te.Details["bytes"])
	assert.EqualValues(t, 100, te.Details["max_bytes"])

	exact := mcp.NewToolResultText(strings.Repeat("x", 100))
	res, err = callLimited(t, context.Background(), time.Second, 100, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return exact, nil
	})
	require.NoError(t, err)
	assert.Same(t, exact, res, "a result of exactly the cap is returned")
}

func TestLimitCallerGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := callLimited(t, ctx, time.Second, 100, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		return mcp.NewToolResultText("late"), nil
	})
	assert.Nil(t, res)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestFailedAccessCheckIsNotLoggedAsADenial(t *testing.T) {
	r := &reader{authorizer: authorizer.AuthorizerFunc(func(ctx context.Context, _ authorizer.Attributes) (authorizer.Decision, string, error) {
		return authorizer.DecisionNoOpinion, "", context.DeadlineExceeded
	})}
	rec := &auditRecord{}
	ctx := context.WithValue(genericapirequest.WithUser(context.Background(), agent), auditKey{}, rec)
	_, err := r.ListVulnerabilityManifests(ctx, "kubescape", metav1.ListOptions{})
	require.Error(t, err)
	assert.False(t, apierrors.IsForbidden(err))
	require.Len(t, rec.reads, 1)
	assert.False(t, rec.reads[0].Allowed)
	assert.Equal(t, context.DeadlineExceeded.Error(), rec.reads[0].Reason)
}

// countingLister counts reads from any goroutine, including a tool's goroutine
// that outlives its call.
type countingLister struct {
	*fakeLister
	reads atomic.Int32
}

func (c *countingLister) List(ctx context.Context, opts *metainternalversion.ListOptions) (runtime.Object, error) {
	c.reads.Add(1)
	return c.fakeLister.List(ctx, opts)
}

type auditLine struct {
	msg    string
	fields map[string]any
}

// captureAudit replaces auditLog for the test and returns its lines.
func captureAudit(t *testing.T) <-chan auditLine {
	t.Helper()
	lines := make(chan auditLine, 10)
	old := auditLog
	auditLog = func(msg string, details ...helpers.IDetails) {
		fields := map[string]any{}
		for _, d := range details {
			fields[d.Key()] = d.Value()
		}
		lines <- auditLine{msg, fields}
	}
	t.Cleanup(func() { auditLog = old })
	return lines
}

func TestAccessCheckThatOutlivesTheDeadline(t *testing.T) {
	lines := captureAudit(t)
	release := make(chan struct{})
	var lister *countingLister
	te := newTestEndpoint(t, "kubescape", func(c *Config) {
		c.CallTimeout = 50 * time.Millisecond
		// kube-apiserver answers the SubjectAccessReview only after the
		// deadline, and allows the read.
		c.Authorizer = authorizer.AuthorizerFunc(func(context.Context, authorizer.Attributes) (authorizer.Decision, string, error) {
			<-release
			return authorizer.DecisionAllow, "", nil
		})
		lister = &countingLister{fakeLister: c.VulnerabilityManifests.(*fakeLister)}
		c.VulnerabilityManifests = lister
	})
	cert := te.frontCA.clientCert(t, frontProxyName)

	text, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.True(t, isError)
	assert.Contains(t, text, `"code":"TIMEOUT"`)

	// The call's line names the check, still pending.
	line := <-lines
	assert.Equal(t, "mcp tool call", line.msg)
	assert.Equal(t, "TIMEOUT", line.fields["result"])
	assert.Equal(t, []read{{Verb: "list", Resource: "vulnerabilitymanifests", Namespace: "kubescape", Pending: true}}, line.fields["reads"])

	// The decision arrives late: it is logged on its own line, and nothing is read.
	close(release)
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("the late decision was not logged")
	}
	assert.Equal(t, "mcp tool call: access check finished after the call ended", line.msg)
	assert.Equal(t, agent.Name, line.fields["user"])
	assert.Equal(t, "list_vulnerability_manifests", line.fields["tool"])
	assert.Equal(t, []read{{Verb: "list", Resource: "vulnerabilitymanifests", Namespace: "kubescape", Allowed: true,
		Reason: "the call ended before the decision; nothing was read"}}, line.fields["reads"])
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, lister.reads.Load(), "no read after the call ended")
	assert.Zero(t, lister.calls)
}

func TestAuditLineWithoutLateChecks(t *testing.T) {
	lines := captureAudit(t)
	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	_, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.False(t, isError)
	line := <-lines
	assert.Equal(t, "ok", line.fields["result"])
	assert.Equal(t, []read{{Verb: "list", Resource: "vulnerabilitymanifests", Namespace: "kubescape", Allowed: true}}, line.fields["reads"])
	select {
	case extra := <-lines:
		t.Fatalf("unexpected second line %q", extra.msg)
	case <-time.After(50 * time.Millisecond):
	}
}
