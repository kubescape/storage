package file

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/stretchr/testify/require"
)

func TestListTimeSeriesExpired_LocalZone(t *testing.T) {
	// Keep these cases serial: time.Local is process-global.
	for _, offset := range []int{2, -2} {
		zone := time.FixedZone("local", offset*60*60)
		t.Run((time.Duration(offset) * time.Hour).String(), func(t *testing.T) {
			previous := time.Local
			time.Local = zone
			t.Cleanup(func() { time.Local = previous })

			synctest.Test(t, func(t *testing.T) {
				// The synctest clock is fixed, independent of wall-clock time.
				pool := NewTestPool(t.TempDir())
				require.NotNil(t, pool)
				defer pool.Close()
				conn, err := pool.Take(context.Background())
				require.NoError(t, err)
				defer pool.Put(conn)

				for name, age := range map[string]time.Duration{
					"fresh": 10 * time.Minute,
					"stale": 2 * time.Hour,
				} {
					stamp := time.Now().UTC().Add(-age).String()
					require.NoError(t, WriteTimeSeriesEntry(conn, "containerprofile", "ns", name, "s", "1", stamp, helpersv1.Learning, helpersv1.Partial, "", true))
				}

				keys, err := ListTimeSeriesExpired(conn, time.Hour)
				require.NoError(t, err)
				require.Equal(t, []string{K8sKeysToPath("", "spdx.softwarecomposition.kubescape.io", "containerprofile", "", "ns", "stale")}, keys,
					"local timezone must neither expire fresh UTC rows nor retain stale UTC rows")
			})
		})
	}
}
