package endpoint

import (
	"testing"

	"github.com/kubescape/storage/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/component-base/metrics/testutil"
)

func TestToolCallsAreCounted(t *testing.T) {
	const tool = "list_vulnerability_manifests"
	count := func(result string) float64 {
		t.Helper()
		v, err := testutil.GetCounterMetricValue(metrics.MCPToolCallsTotal.WithLabelValues(tool, result))
		require.NoError(t, err)
		return v
	}
	observed := func() uint64 {
		t.Helper()
		n, err := testutil.GetHistogramMetricCount(metrics.MCPToolCallDuration.WithLabelValues(tool))
		require.NoError(t, err)
		return n
	}
	ok, denied, calls := count("ok"), count("RBAC_DENIED"), observed()

	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	_, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.False(t, isError)
	_, isError = toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{}`)))
	require.True(t, isError)

	assert.Equal(t, ok+1, count("ok"))
	assert.Equal(t, denied+1, count("RBAC_DENIED"))
	assert.Equal(t, calls+2, observed())
	size, err := testutil.GetHistogramMetricCount(metrics.MCPToolResponseBytes.WithLabelValues(tool))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, uint64(2))
}

func TestAccessChecksAreCounted(t *testing.T) {
	count := func(decision string) float64 {
		t.Helper()
		v, err := testutil.GetCounterMetricValue(metrics.MCPAccessChecksTotal.WithLabelValues(decision, "hit"))
		require.NoError(t, err)
		return v
	}
	allowed, denied := count("allowed"), count("denied")

	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	_, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.False(t, isError)
	_, isError = toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"team-a"}`)))
	require.True(t, isError)

	assert.Equal(t, allowed+1, count("allowed"))
	assert.Equal(t, denied+1, count("denied"))
}
