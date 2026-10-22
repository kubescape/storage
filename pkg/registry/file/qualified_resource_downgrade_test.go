package file

import (
	"bytes"
	"context"
	"encoding/gob"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

// PREFIX_OLD_READER is built from the pinned pre-fix revision by the documented
// local verification recipe. It is deliberately a separate process and binary.
func TestQualifiedResourcePinnedOldReaderDowngrade(t *testing.T) {
	reader := os.Getenv("PREFIX_OLD_READER")
	if reader == "" {
		t.Skip("set PREFIX_OLD_READER to probe built from 7c93d2142e50c438bafeb5e57e26ff50c8701081")
	}
	root := t.TempDir()
	for _, name := range []string{"updated", "deleted"} {
		out, err := exec.Command(reader, root, "create", name).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	ctx := context.Background()
	db := filepath.Join(root, "metadata.sq3")
	pool := NewPool(db, 3, 0)
	fs := afero.NewOsFs()
	sch := runtime.NewScheme()
	install.Install(sch)
	gate, err := NewWriteGate(ctx, pool)
	require.NoError(t, err)
	_, err = MigrateQualifiedResourcePrefixes(ctx, pool, gate, fs, root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
	_, err = MigrateContainerProfiles(ctx, pool, gate, fs, root, sch, ContainerProfileMigrationOptions{})
	require.NoError(t, err)
	legacy := NewStorageImpl(fs, root, pool, nil, sch)
	processor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "ns", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	store, err := NewObjectStore(pool, db, nil, sch, processor, legacy, gate, ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(t, err)
	key := "/" + softwarecomposition.GroupName + "/containerprofiles/ns/"
	require.NoError(t, store.GuaranteedUpdate(ctx, key+"updated", &softwarecomposition.ContainerProfile{}, false, nil, func(obj runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		p := obj.(*softwarecomposition.ContainerProfile)
		p.Spec.ImageTag = "latest:2"
		return p, nil, nil
	}, nil))
	require.NoError(t, store.Delete(ctx, key+"deleted", &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))
	require.NoError(t, store.Close())
	require.NoError(t, gate.Close())
	require.NoError(t, pool.Close())
	pool = NewPool(db, 2, 0)
	report, err := ExportContainerProfiles(ctx, pool, fs, root, sch, ContainerProfileExportOptions{})
	require.NoError(t, err)
	require.Zero(t, report.Undecodable, "do not reverse prefixes after an incomplete export")
	require.Equal(t, 1, report.StaleFilesRemoved)
	// Read Gob directly so the native-payload fallback cannot conceal bad export.
	payload, err := os.ReadFile(filepath.Join(root, key+"updated.g"))
	require.NoError(t, err)
	var exported softwarecomposition.ContainerProfile
	require.NoError(t, gob.NewDecoder(bytes.NewReader(payload)).Decode(&exported))
	require.Equal(t, "latest:2", exported.Spec.ImageTag)
	_, err = MigrateQualifiedResourcePrefixes(ctx, pool, nil, fs, root, QualifiedResourceMigrationOptions{Direction: "rollback"})
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	out, err := exec.Command(reader, root, "get", "updated").CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Contains(t, string(out), "latest:2")
	out, err = exec.Command(reader, root, "get", "deleted").CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", out)
	require.Equal(t, 4, exit.ExitCode())
}
