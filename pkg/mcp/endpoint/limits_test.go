package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
