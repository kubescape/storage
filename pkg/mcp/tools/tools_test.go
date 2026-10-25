package tools

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolsAreMarkedReadOnly(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	Register(s, &fakeReader{})
	require.NotEmpty(t, s.ListTools())
	for name, tool := range s.ListTools() {
		a := tool.Tool.Annotations
		require.NotNil(t, a.ReadOnlyHint, name)
		require.NotNil(t, a.DestructiveHint, name)
		require.NotNil(t, a.OpenWorldHint, name)
		assert.True(t, *a.ReadOnlyHint, name)
		assert.False(t, *a.DestructiveHint, name)
		assert.False(t, *a.OpenWorldHint, name)
	}
}
