package main

// cpexport writes every ContainerProfile the SQLite-native backend
// (config.ContainerProfileSqliteBackend) holds in the payloads table back
// as the legacy gob payload file an older storage binary expects. It is the
// first half of the rollback order of
// .omc/plans/full-acid-storage-architecture.md §8.4 — export, THEN
// downgrade — because an older binary's get() deletes the metadata row of
// any ContainerProfile whose file is missing, destroying the object.
//
// Run it with the storage server stopped (scale the deployment to 0, then
// run it against the PVC), never alongside a serving process: a write that
// lands after a key was exported leaves the old binary a stale file.
//
//	cpexport [-db /data/metadata.sq3] [-root /data] [-dry-run]

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
)

func main() {
	root := flag.String("root", file.DefaultStorageRoot, "storage root directory holding the payload files")
	db := flag.String("db", "", "SQLite database path (default <root>/metadata.sq3)")
	dryRun := flag.Bool("dry-run", false, "decode and count without writing any file")
	batch := flag.Int("batch", file.DefaultMigrationBatchSize, "rows read per query")
	flag.Parse()
	if *db == "" {
		*db = filepath.Join(*root, "metadata.sq3")
	}

	sch := runtime.NewScheme()
	install.Install(sch)
	pool := file.NewPoolWithOptions(*db, file.PoolOptions{Size: 2, BusyTimeout: file.DefaultBusyTimeout})
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	report, err := file.ExportContainerProfiles(ctx, pool, afero.NewOsFs(), *root, sch, file.ContainerProfileExportOptions{BatchSize: *batch, DryRun: *dryRun})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cpexport: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("cpexport: exported=%d legacySkipped=%d undecodable=%d dryRun=%v elapsed=%s\n",
		report.Exported, report.LegacySkipped, report.Undecodable, report.DryRun, report.Elapsed)
	if report.Undecodable > 0 {
		os.Exit(2)
	}
}
