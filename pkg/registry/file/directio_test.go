package file

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncw/directio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file written through DirectIOWriter ends where the data ends. Before, Close cut the
// file at the bytes written plus the last partial block once more, so every file carried
// up to a block of stale tail: ten bytes became twenty, and a whole block was written twice.
func TestDirectIOWriterFileEndsWithTheData(t *testing.T) {
	for _, size := range []int{10, directio.BlockSize, directio.BlockSize + 904, 2*directio.BlockSize + 1808, 3 * directio.BlockSize} {
		path := filepath.Join(t.TempDir(), "payload")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		require.NoError(t, err)
		payload := bytes.Repeat([]byte("0123456789"), size/10+1)[:size]
		w := NewDirectIOWriter(f)
		n, err := w.Write(payload)
		require.NoError(t, err)
		require.Equal(t, size, n)
		require.NoError(t, w.Close())
		require.NoError(t, f.Close())
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, got, size, "size %d", size)
		assert.Equal(t, payload, got, "size %d", size)
	}
}
