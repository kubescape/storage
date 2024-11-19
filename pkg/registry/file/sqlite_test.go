package file

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func TestMemoryConn(t *testing.T) {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "name",
		},
	}
	// Prepare pool and connection
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	conn, err := pool.Take(context.TODO())
	require.NoError(t, err)
	// Test write
	assert.NoError(t, writeMetadata(conn, "/v1/pods/default1/pod1", &pod))
	assert.NoError(t, writeMetadata(conn, "/v1/pods/default1/pod2", &pod))
	assert.NoError(t, writeMetadata(conn, "/v1/pods/default2/pod1", &pod))
	// Test count
	count, err := countMetadata(conn, "/v1/pods")
	assert.NoError(t, err)
	assert.Equal(t, int64(3), count)
	// Test count namespace
	count, err = countMetadata(conn, "/v1/pods/default1")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)
	// Test list
	list, last, err := listMetadata(conn, "/v1/pods", "", int64(500))
	assert.NoError(t, err)
	assert.Len(t, list, 3)
	expected := []string{
		"{\"metadata\":{\"name\":\"name\",\"creationTimestamp\":null},\"spec\":{\"containers\":null},\"status\":{}}",
		"{\"metadata\":{\"name\":\"name\",\"creationTimestamp\":null},\"spec\":{\"containers\":null},\"status\":{}}",
		"{\"metadata\":{\"name\":\"name\",\"creationTimestamp\":null},\"spec\":{\"containers\":null},\"status\":{}}",
	}
	assert.Equal(t, expected, list)
	assert.Equal(t, "3", last)
	// Test list with limit
	list, last, err = listMetadata(conn, "/v1/pods/default1", "", int64(1))
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "1", last)
	// Test list with last
	list, last, err = listMetadata(conn, "/v1/pods/default1", last, int64(500))
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "2", last)
	// Test read
	b, err := ReadMetadata(conn, "/v1/pods/default2/pod1")
	assert.NoError(t, err)
	assert.Equal(t, "{\"metadata\":{\"name\":\"name\",\"creationTimestamp\":null},\"spec\":{\"containers\":null},\"status\":{}}", string(b))
	// Test delete returning value
	p := &v1.Pod{}
	assert.NoError(t, DeleteMetadata(conn, "/v1/pods/default1/pod1", p))
	assert.NotNil(t, p)
	assert.Equal(t, "name", pod.Name)
	// Tear down
	pool.Put(conn)
	assert.NoError(t, pool.Close())
}

func Test_pathToKindKey(t *testing.T) {

	tests := []struct {
		test      string
		path      string
		kind      string
		namespace string
		name      string
	}{
		{
			test:      "single",
			path:      "/spdx.softwarecomposition.kubescape.io/applicationprofiles/default/replicaset-collection-85f89d8b47",
			kind:      "applicationprofiles",
			namespace: "default",
			name:      "replicaset-collection-85f89d8b47",
		},
		{
			test:      "namespace",
			path:      "/spdx.softwarecomposition.kubescape.io/applicationprofiles/default",
			kind:      "applicationprofiles",
			namespace: "default",
		},
		{
			test: "cluster",
			path: "/spdx.softwarecomposition.kubescape.io/applicationprofiles",
			kind: "applicationprofiles",
		},
	}
	for _, tt := range tests {
		t.Run(tt.test, func(t *testing.T) {
			kind, namespace, name := pathToKeys(tt.path)
			assert.Equalf(t, tt.kind, kind, "pathToKeys(%v)", tt.path)
			assert.Equalf(t, tt.namespace, namespace, "pathToKeys(%v)", tt.path)
			assert.Equalf(t, tt.name, name, "pathToKeys(%v)", tt.path)
		})
	}
}
