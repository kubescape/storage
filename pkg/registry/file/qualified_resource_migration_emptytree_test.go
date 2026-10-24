package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestQualifiedResourceMigrationIgnoresEmptyLeftoverSourceTree(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	group := filepath.Join(root, softwarecomposition.GroupName)

	source := filepath.Join(group, "containerprofile", "ns1", "object.g")
	require.NoError(t, os.MkdirAll(filepath.Dir(source), 0755))
	require.NoError(t, os.WriteFile(source, []byte("x"), 0644))
	prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES(?,'ns1','x')`, "containerprofile")

	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(group, "containerprofiles", "ns1", "object.g"))
	require.NoError(t, err, "the forward migration moved the source tree")

	for _, ns := range []string{"ns1", "ns2", "ns3"} {
		require.NoError(t, os.MkdirAll(filepath.Join(group, "containerprofile", ns), 0755))
	}

	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err, "empty namespace directories left behind by an older writer must not block the migration")
	_, err = os.Stat(filepath.Join(group, "containerprofile"))
	require.True(t, os.IsNotExist(err), "the empty leftover source tree is removed")
	_, err = os.Stat(filepath.Join(group, "containerprofiles", "ns1", "object.g"))
	require.NoError(t, err, "the migrated data is untouched")
}

func TestQualifiedResourceMigrationEmptySourceTreeBesidePopulatedDestinationWithoutMarker(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	group := filepath.Join(root, softwarecomposition.GroupName)

	destination := filepath.Join(group, "containerprofiles", "ns1", "object.g")
	require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0755))
	require.NoError(t, os.WriteFile(destination, []byte("x"), 0644))
	prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES(?,'ns1','x')`, "containerprofiles")
	require.NoError(t, os.MkdirAll(filepath.Join(group, "containerprofile", "ns1"), 0755))

	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err, "a source prefix holding only empty directories is not a conflicting population")
	_, err = os.Stat(filepath.Join(group, "containerprofile"))
	require.True(t, os.IsNotExist(err))
}
