package file

// The startup migration (§8.2) and the reverse export (§8.4) at a realistic
// scale: ~5,000 ContainerProfile rows of every reconcile shape in production
// proportions, seeded through the legacy StorageImpl over real files, run
// with the production batch size. The small-scale tests in
// sqliteobject_migration_test.go pin each shape's semantics; these pin that
// the counts, INV-2, idempotence, dry-run prediction, crash resumption and
// the export hold together on a corpus, and report the cost.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// scaleMix is the corpus shape: counts per reconcile outcome. The
// proportions are the assessment's (~95 % clean, ~2 % diverged, ~1 % each
// row/file-only, ~0.5 % each staging and orphan, a handful undecodable, a few
// wrong-type gobs through the external tool).
type scaleMix struct {
	plain        int // clean, non-TS
	series       int // clean TS series of two reports each
	diverged     int // row resourceVersion ahead of the file
	rowOnly      int // row without file
	fileOnly     int // file without row
	stagingPairs int // objects named *.g.tail with staging siblings + plain staging files
	orphans      int // payloads rows without a metadata row
	garbageRow   int // undecodable gob, row present
	garbageNoRow int // undecodable gob, no row (keeps the sweep armed)
	toolOK       int // wrong-type gob with a row, the external tool decodes it
	toolNoRow    int // wrong-type gob without a row, the tool decodes it (sweep)
	toolFail     int // wrong-type gob with a row, the tool fails
}

func defaultScaleMix() scaleMix {
	return scaleMix{
		plain: 4300, series: 260,
		diverged: 100, rowOnly: 50, fileOnly: 50,
		stagingPairs: 12, orphans: 25,
		garbageRow: 5, garbageNoRow: 2,
		toolOK: 4, toolNoRow: 1, toolFail: 2,
	}
}

// rows is the number of metadata rows the legacy binary leaves behind.
func (m scaleMix) rows() int {
	return m.plain + 2*m.series + m.diverged + m.rowOnly + m.stagingPairs + m.garbageRow + m.toolOK + m.toolFail
}

// expectedCounts is the report the migration must produce for the mix.
func (m scaleMix) expectedCounts() map[string]int {
	return map[string]int{
		MigrationShapeMigrated:                                m.plain + 2*m.series + m.stagingPairs + m.toolOK,
		MigrationShapeDiverged:                                m.diverged,
		MigrationShapeRowWithoutFile:                          m.rowOnly,
		MigrationShapeFileWithoutRow:                          m.fileOnly + m.toolNoRow,
		MigrationShapeTempFile:                                3 * m.stagingPairs,
		MigrationShapeOrphanPayload:                           m.orphans,
		MigrationShapeUndecodable + "/" + MigrationSourceFile: m.garbageRow + m.garbageNoRow + m.toolFail,
	}
}

// scaleCorpus is what the seeding left: every key with the object the legacy
// binary served for it (nil for keys that must not survive), plus the keys
// per shape for targeted assertions.
type scaleCorpus struct {
	mix       scaleMix
	before    map[string]*softwarecomposition.ContainerProfile
	gone      []string // rowOnly + orphans: no object after the migration
	unchanged []string // garbageRow + toolFail: row left as is (rv NULL)
	files     int      // payload files the legacy binary wrote
}

// installSidecarMigrationTool points execMigrationTool at a script that
// answers from a JSON sidecar named after the payload file's basename, and
// fails when there is none: the tool-succeeds and tool-fails arms of
// decodeLegacyFile from one fixture.
func installSidecarMigrationTool(t *testing.T) (sidecarDir string) {
	t.Helper()
	sidecarDir = t.TempDir()
	script := filepath.Join(sidecarDir, "fake-migration.sh")
	body := "#!/bin/sh\n" +
		"# args: -file <path> -type <type>\n" +
		"f=\"$FAKE_SIDECAR_DIR/$(basename \"$2\").json\"\n" +
		"if [ ! -f \"$f\" ]; then echo \"fake migration: no sidecar for $2\" >&2; exit 1; fi\n" +
		"cat \"$f\"\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0755))
	t.Setenv("FAKE_SIDECAR_DIR", sidecarDir)
	old := migrationBinaryPath
	migrationBinaryPath = script
	t.Cleanup(func() { migrationBinaryPath = old })
	return sidecarDir
}

// seedScaleCorpus writes the mix through the old binary and then applies
// each shape's damage (row edits through the fixture, file removals,
// staging and garbage files). Objects rotate through every template.
func (e *migrationEnv) seedScaleCorpus(mix scaleMix, sidecarDir string) *scaleCorpus {
	t := e.t
	t.Helper()
	tpls := loadTemplates(t) // every testdata/p*.json (containerprofile_load_test.go)
	c := &scaleCorpus{mix: mix, before: map[string]*softwarecomposition.ContainerProfile{}}
	rng := rand.New(rand.NewSource(1))
	next := 0
	obj := func(name string, ts bool) *softwarecomposition.ContainerProfile {
		p := tpls[next%len(tpls)].profile.DeepCopy()
		next++
		p.Name = name
		p.Namespace = e.ns
		p.ResourceVersion = ""
		p.UID = uuid.NewUUID()
		if !ts {
			delete(p.Annotations, helpersv1.ReportSeriesIdMetadataKey)
		}
		return p
	}
	create := func(p *softwarecomposition.ContainerProfile) string {
		out := e.legacyCreate(p)
		c.before[e.key(out.Name)] = out
		c.files++
		return e.key(out.Name)
	}

	for i := 0; i < mix.plain; i++ {
		k := create(obj(fmt.Sprintf("plain-%05d", i), false))
		// A fifth of the clean rows sit at RV 2 or 3, as a live cluster's do.
		if i%5 == 0 {
			for n := 0; n < 1+i%2; n++ {
				c.before[k] = e.legacyUpdate(k, func(cp *softwarecomposition.ContainerProfile) {
					cp.Annotations["scale-test/updated"] = strconv.Itoa(n + 1)
				})
			}
		}
	}
	for i := 0; i < mix.series; i++ {
		base := fmt.Sprintf("replicaset-scale-%04d-c-1111-2222", i)
		for _, suffix := range []string{"0001", "0002"} {
			create(obj(base+"-"+suffix, true))
		}
	}
	for i := 0; i < mix.diverged; i++ {
		k := create(obj(fmt.Sprintf("diverged-%03d", i), false))
		setRowJSON(t, e.fixture, k, `$.resourceVersion`, strconv.Itoa(5+rng.Intn(20)))
	}
	for i := 0; i < mix.rowOnly; i++ {
		k := create(obj(fmt.Sprintf("rowonly-%03d", i), false))
		require.NoError(t, e.fs.Remove(e.filePath(filepath.Base(k))))
		c.gone = append(c.gone, k)
		delete(c.before, k)
		c.files--
	}
	for i := 0; i < mix.fileOnly; i++ {
		k := create(obj(fmt.Sprintf("fileonly-%03d", i), false))
		if i%2 == 0 {
			c.before[k] = e.legacyUpdate(k, func(cp *softwarecomposition.ContainerProfile) { cp.Annotations["scale-test/updated"] = "1" })
		}
		_, _, kind, _, ns, name := K8sPathToKeys(k)
		require.NoError(t, sqlitex.Execute(e.fixture, `DELETE FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`, &sqlitex.ExecOptions{Args: []any{kind, ns, name}}))
	}
	dir := filepath.Dir(e.filePath("x"))
	for i := 0; i < mix.stagingPairs; i++ {
		// A workload legally named "*.g.tail": its payload file name contains
		// ".g.t" and must survive; its staging siblings must not.
		name := fmt.Sprintf("trap-%03d.g.tail", i)
		create(obj(name, false))
		for _, n := range []string{name + ".g.t", fmt.Sprintf("%s.g.t.%d.%d", name, 1700000000000000000+i, i), fmt.Sprintf("staged-%03d.g.t", i)} {
			require.NoError(t, afero.WriteFile(e.fs, filepath.Join(dir, n), []byte("staged"), 0644))
		}
	}
	for i := 0; i < mix.orphans; i++ {
		name := fmt.Sprintf("orphan-%03d", i)
		require.NoError(t, sqlitex.Execute(e.fixture,
			`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{ContainerProfileKind, e.ns, name, PayloadEncodingJSONV1Beta1, []byte("{}")}}))
		c.gone = append(c.gone, e.key(name))
	}
	for i := 0; i < mix.garbageRow; i++ {
		k := create(obj(fmt.Sprintf("garbage-%03d", i), false))
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(filepath.Base(k)), []byte("not a gob stream"), 0644))
		c.unchanged = append(c.unchanged, k)
		delete(c.before, k)
	}
	for i := 0; i < mix.garbageNoRow; i++ {
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(fmt.Sprintf("garbage-norow-%03d", i)), []byte("not a gob stream"), 0644))
		c.files++
	}
	wrongType := gobPayloadNeedingMigration(t)
	sidecar := func(k string) {
		// What the real tool would print: the object as JSON.
		b, err := json.Marshal(c.before[k])
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(sidecarDir, filepath.Base(k)+GobExt+".json"), b, 0644))
	}
	for i := 0; i < mix.toolOK; i++ {
		k := create(obj(fmt.Sprintf("toolok-%03d", i), false))
		sidecar(k)
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(filepath.Base(k)), wrongType, 0644))
	}
	for i := 0; i < mix.toolNoRow; i++ {
		k := create(obj(fmt.Sprintf("toolnorow-%03d", i), false))
		sidecar(k)
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(filepath.Base(k)), wrongType, 0644))
		_, _, kind, _, ns, name := K8sPathToKeys(k)
		require.NoError(t, sqlitex.Execute(e.fixture, `DELETE FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`, &sqlitex.ExecOptions{Args: []any{kind, ns, name}}))
	}
	for i := 0; i < mix.toolFail; i++ {
		k := create(obj(fmt.Sprintf("toolfail-%03d", i), false))
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(filepath.Base(k)), wrongType, 0644))
		c.unchanged = append(c.unchanged, k)
		delete(c.before, k)
	}
	return c
}

// assertScaleMigrated checks every surviving key against what the old
// binary served, INV-2 on every CP key, and the damaged keys' fates.
func (e *migrationEnv) assertScaleMigrated(c *scaleCorpus) {
	t := e.t
	t.Helper()
	for k, legacyObj := range c.before {
		got, err := e.storeGet(k)
		require.NoError(t, err, k)
		require.Equal(t, legacyObj.UID, got.UID, k)
		want := legacyObj
		if strings.Contains(k, "/diverged-") {
			// The one intended difference: the row said 5..24, the file 1;
			// the object is served at max+1 (the row JSON was rewritten to it).
			require.Equal(t, strconv.FormatInt(parseRV(e.inspect(k).jsonRV), 10), got.ResourceVersion, k)
			require.Greater(t, parseRV(got.ResourceVersion), int64(5), "%s: max(rowRV, payloadRV)+1", k)
			want = legacyObj.DeepCopy()
			want.ResourceVersion = got.ResourceVersion
		} else {
			require.Equal(t, legacyObj.ResourceVersion, got.ResourceVersion, k)
		}
		require.Equal(t, canonicalCP(want), canonicalCP(got), "the ObjectStore serves what the old binary served for %s", k)
	}
	for _, k := range c.gone {
		row := e.inspect(k)
		require.False(t, row.metaExists || row.payloadExists, "%s must not survive", k)
	}
	for _, k := range c.unchanged {
		row := e.inspect(k)
		require.True(t, row.metaExists && row.rv == nil && !row.payloadExists, "%s: an undecodable row is left exactly as it was", k)
		exists, err := afero.Exists(e.fs, e.filePath(filepath.Base(k)))
		require.NoError(t, err)
		require.True(t, exists, "%s: an undecodable file is never deleted (PM-2)", k)
	}
	keys := allCPKeys(t, e.fixture)
	require.Equal(t, len(c.before)+len(c.unchanged), len(keys), "exactly the surviving objects and the untouched undecodable rows remain")
	for _, k := range keys {
		if e.inspect(k).rv == nil {
			continue // an unchanged undecodable row
		}
		assertINV2(t, e.fixture, k)
	}
}

// memReport is the memory cost of one phase: heap growth and the process's
// peak resident set (Linux VmHWM; 0 elsewhere).
type memReport struct {
	heapAllocBefore, heapAllocAfter, totalAllocDelta, sysAfter, peakRSS uint64
}

func (m memReport) String() string {
	return fmt.Sprintf("heapAlloc %dMB→%dMB totalAlloc +%dMB sys %dMB peakRSS %dMB",
		m.heapAllocBefore>>20, m.heapAllocAfter>>20, m.totalAllocDelta>>20, m.sysAfter>>20, m.peakRSS>>20)
}

func peakRSS() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "VmHWM:") {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}

func measureMem(fn func()) memReport {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return memReport{
		heapAllocBefore: before.HeapAlloc, heapAllocAfter: after.HeapAlloc,
		totalAllocDelta: after.TotalAlloc - before.TotalAlloc, sysAfter: after.Sys, peakRSS: peakRSS(),
	}
}

// treeSize sums the sizes of every file under dir on the OS filesystem.
func treeSize(t *testing.T, dir string) (files int, bytes int64) {
	t.Helper()
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files++
			bytes += info.Size()
		}
		return nil
	})
	require.NoError(t, err)
	return files, bytes
}

func dbSize(t *testing.T, dbPath string) int64 {
	t.Helper()
	var total int64
	for _, p := range []string{dbPath, dbPath + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			total += st.Size()
		}
	}
	return total
}

// scaleMigrationBound is the elapsed-time ceiling for the 5,000-row
// migration. Measured at ~1.4 s on a laptop (tmpfs); a 40× margin absorbs a
// slow CI runner while still failing a per-row full scan or a per-row
// checkpoint, either of which is minutes at this size. The race detector
// stretches the run ~20× (25 s measured), so its bound is four times wider.
func scaleMigrationBound() time.Duration {
	if raceDetectorEnabled {
		return 4 * time.Minute
	}
	return time.Minute
}

// TestMigration_Scale_MixedCorpus: 5,000 rows of every shape, production
// batch size, exact counts, INV-2 everywhere, dry-run prediction, idempotent
// second run, elapsed under the bound, memory reported.
func TestMigration_Scale_MixedCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	base := t.TempDir()
	e := newMigrationEnv(t, afero.NewBasePathFs(afero.NewOsFs(), base))
	sidecars := installSidecarMigrationTool(t)
	mix := defaultScaleMix()
	seedStart := time.Now()
	c := e.seedScaleCorpus(mix, sidecars)
	t.Logf("seeded %d metadata rows, %d payload files, %d time_series rows in %s", mix.rows(), c.files, 2*mix.series, time.Since(seedStart))
	tsRowsBefore := countTimeSeriesRows(t, e.fixture)
	require.GreaterOrEqual(t, tsRowsBefore, 500)

	// Dry-run first, with no gate (the flag-off operator check of §8.3).
	dry, err := MigrateContainerProfiles(e.ctx, e.pool, nil, e.fs, DefaultStorageRoot, e.scheme, ContainerProfileMigrationOptions{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, mix.expectedCounts(), dry.Counts, "dry-run counts")
	require.Nil(t, e.inspect(e.key("plain-00000")).rv, "dry-run wrote nothing")

	e.startNew()
	var report *ContainerProfileMigrationReport
	mem := measureMem(func() {
		report = e.mustMigrate(ContainerProfileMigrationOptions{}) // production batch size
	})
	t.Logf("migration: elapsed=%s batches=%d counts=%v mem: %s", report.Elapsed, report.Batches, report.Counts, mem)
	require.Less(t, report.Elapsed, scaleMigrationBound())
	require.Equal(t, mix.expectedCounts(), report.Counts, "real-run counts")
	require.Equal(t, dry.Counts, report.Counts, "the dry run predicted the real run exactly")
	require.GreaterOrEqual(t, report.Batches, (mix.rows()+DefaultMigrationBatchSize-1)/DefaultMigrationBatchSize, "at least ceil(rows/%d) batches", DefaultMigrationBatchSize)
	require.True(t, report.SweepsRun)
	require.False(t, e.migrationDone(), "undecodable files without a row keep the sweep armed (PM-2)")
	require.Equal(t, tsRowsBefore, countTimeSeriesRows(t, e.fixture), "time_series rows untouched")

	verifyStart := time.Now()
	e.assertScaleMigrated(c)
	t.Logf("verified %d surviving objects + INV-2 on every key in %s", len(c.before), time.Since(verifyStart))

	// The migrated rows are live: a CAS update on a sample of every clean shape.
	for _, name := range []string{"plain-00000", "plain-04299", "replicaset-scale-0000-c-1111-2222-0001", "diverged-000", "trap-000.g.tail", "toolok-000", "fileonly-001", "toolnorow-000"} {
		require.NoError(t, e.storeUpdate(e.ctx, e.key(name)), name)
		assertINV2(t, e.fixture, e.key(name))
	}

	// Second run: only the undecodable rows are met again; nothing is written.
	again := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 0, again.Batches, "a second run commits nothing: %v", again.Counts)
	for shape, n := range again.Counts {
		if strings.HasPrefix(shape, MigrationShapeUndecodable) {
			continue
		}
		require.Equal(t, 0, n, "second run reconciled %s", shape)
	}
	require.Equal(t, report.Count(MigrationShapeUndecodable), again.Count(MigrationShapeUndecodable), "undecodable payloads stay visible on every start")
	t.Logf("second run: elapsed=%s counts=%v", again.Elapsed, again.Counts)
}

func countTimeSeriesRows(t *testing.T, conn *sqlite.Conn) int {
	t.Helper()
	var n int
	require.NoError(t, sqlitex.Execute(conn, `SELECT count(*) FROM time_series`, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { n = int(stmt.ColumnInt64(0)); return nil }}))
	return n
}

// TestMigration_Scale_ResumesAfterProcessKill is TestMigration_ResumesAfter
// ProcessKill on the 5,000-row corpus: the child dies inside batch 40 of the
// production batch size, the parent resumes and completes.
func TestMigration_Scale_ResumesAfterProcessKill(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	if os.Getenv("CP_MIGRATION_CRASH_DIR") != "" {
		t.Skip("child helper only")
	}
	base := t.TempDir()
	fs := afero.NewBasePathFs(afero.NewOsFs(), base)
	e := newMigrationEnv(t, fs)
	sidecars := installSidecarMigrationTool(t)
	mix := defaultScaleMix()
	c := e.seedScaleCorpus(mix, sidecars)
	require.NoError(t, e.legacyPool.Close())
	require.NoError(t, e.pool.Close())
	e.pool, e.legacyPool = nil, nil

	const crashBatch = 40
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(),
		"CP_MIGRATION_CRASH_DIR="+base, "CP_MIGRATION_CRASH_DB="+e.dbPath, "CP_MIGRATION_CRASH_TOOL="+migrationBinaryPath,
		"CP_MIGRATION_CRASH_BATCH="+strconv.Itoa(crashBatch), "CP_MIGRATION_CRASH_BATCHSIZE="+strconv.Itoa(DefaultMigrationBatchSize))
	childStart := time.Now()
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "the child must die: %v\n%s", err, out)
	require.Equal(t, 3, exitErr.ExitCode(), "%s", out)
	require.Contains(t, string(out), fmt.Sprintf("child: crashing inside batch %d", crashBatch))
	t.Logf("child ran %d batches and died in %s", crashBatch-1, time.Since(childStart))

	pool := NewPoolWithOptions(e.dbPath, PoolOptions{Size: 4, BusyTimeout: 5 * time.Second, DisableAutoCheckpoint: true})
	armUngatedWriteCheck(t, pool)
	e.pool = pool
	e.fixture = openFixtureConn(t, pool, e.dbPath, 5*time.Second)
	// Every candidate row is either fully reconciled or untouched; the rows
	// the child's committed batches covered are exactly (crashBatch-1)*B
	// candidates — some of which are row_without_file deletes (no row left)
	// rather than rewrites, so count outcomes, not rewrites.
	durable, pending := 0, 0
	for _, k := range allCPKeys(t, e.fixture) {
		row := e.inspect(k)
		if !row.metaExists {
			continue
		}
		require.Equal(t, row.rv != nil, row.payloadExists, "no half-written key after the kill: %s", k)
		if row.rv != nil {
			durable++
		} else {
			pending++
		}
	}
	require.Greater(t, durable, 0)
	require.Greater(t, pending, 0)
	t.Logf("after the kill: %d rows durable, %d pending", durable, pending)

	e.startNew()
	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	t.Logf("resumed: elapsed=%s batches=%d counts=%v", report.Elapsed, report.Batches, report.Counts)
	require.Less(t, report.Elapsed, scaleMigrationBound())
	want := mix.expectedCounts()
	// The child's committed batches covered the first (crashBatch-1)*B
	// candidate rows in rowid order (clean plain rows: seeded first); the
	// resume reconciles every other row candidate, and the sweeps (file
	// shapes) run for the first time.
	require.Equal(t, (crashBatch-1)*DefaultMigrationBatchSize, durable, "exactly the child's committed batches are durable")
	rowCandidates := want[MigrationShapeMigrated] + want[MigrationShapeDiverged] + want[MigrationShapeRowWithoutFile]
	resumedRows := report.Count(MigrationShapeMigrated) + report.Count(MigrationShapeDiverged) + report.Count(MigrationShapeRowWithoutFile)
	require.Equal(t, rowCandidates-durable, resumedRows, "the resume reconciles exactly the rows the child had not committed: %v", report.Counts)
	require.Equal(t, want[MigrationShapeFileWithoutRow], report.Count(MigrationShapeFileWithoutRow))
	require.Equal(t, want[MigrationShapeTempFile], report.Count(MigrationShapeTempFile))
	require.Equal(t, want[MigrationShapeOrphanPayload], report.Count(MigrationShapeOrphanPayload))
	e.assertScaleMigrated(c)
	again := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 0, again.Batches, "%v", again.Counts)
}

// ---- the export at scale, through the real cpexport binary ----

var (
	cpexportBinOnce sync.Once
	cpexportBinPath string
	cpexportBinErr  error
)

// buildCpexport compiles cmd/cpexport once per test binary.
func buildCpexport(t *testing.T) string {
	t.Helper()
	cpexportBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cpexport-bin")
		if err != nil {
			cpexportBinErr = err
			return
		}
		bin := filepath.Join(dir, "cpexport")
		cmd := exec.Command("go", "build", "-o", bin, "github.com/kubescape/storage/cmd/cpexport")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			cpexportBinErr = fmt.Errorf("%w: %s", err, stderr.String())
			return
		}
		cpexportBinPath = bin
	})
	require.NoError(t, cpexportBinErr, "go build cmd/cpexport")
	return cpexportBinPath
}

var cpexportReportRE = regexp.MustCompile(`cpexport: exported=(\d+) legacySkipped=(\d+) undecodable=(\d+) dryRun=(true|false) elapsed=(\S+)`)

type cpexportResult struct {
	exitCode                             int
	exported, legacySkipped, undecodable int
	dryRun                               bool
	stdout, stderr                       string
}

// runCpexport runs the built binary and parses its report line.
func runCpexport(t *testing.T, bin string, args ...string) cpexportResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := cpexportResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("run cpexport: %v", err)
	}
	if m := cpexportReportRE.FindStringSubmatch(res.stdout); m != nil {
		res.exported, _ = strconv.Atoi(m[1])
		res.legacySkipped, _ = strconv.Atoi(m[2])
		res.undecodable, _ = strconv.Atoi(m[3])
		res.dryRun = m[4] == "true"
	}
	return res
}

// TestExport_Scale_BinaryAgainstMigratedCorpus: migrate the 5,000-row corpus,
// then run the real cpexport binary against the root (dry-run, then for
// real), and prove every exported file decodes to what the ObjectStore
// serves. Files and payload rows coexist during the rollback window; the
// disk overhead of that window is reported.
func TestExport_Scale_BinaryAgainstMigratedCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	bin := buildCpexport(t)
	base := t.TempDir()
	e := newMigrationEnv(t, afero.NewBasePathFs(afero.NewOsFs(), base))
	sidecars := installSidecarMigrationTool(t)
	mix := defaultScaleMix()
	c := e.seedScaleCorpus(mix, sidecars)
	e.startNew()
	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, mix.expectedCounts(), report.Counts)
	// The store keeps writing after the flip: keys the old binary has no
	// file for (created) or a stale file for (updated) are what the export
	// exists for.
	created := 200
	for i := 0; i < created; i++ {
		e.storeCreate(fmt.Sprintf("created-under-the-new-store-%03d", i))
	}
	updated := 300
	for i := 0; i < updated; i++ {
		require.NoError(t, e.storeUpdate(e.ctx, e.key(fmt.Sprintf("plain-%05d", i))))
	}
	// The server is stopped for the export (the binary's contract).
	e.stopNew()

	root := filepath.Join(base, DefaultStorageRoot)
	filesBefore, bytesBefore := treeSize(t, root)
	dbBefore := dbSize(t, e.dbPath)
	// Every migrated row plus the store's own writes. The rows the migration
	// left rv NULL have no payloads row and are outside the export's join
	// entirely (their file is the only copy and stays); LegacySkipped counts
	// only rv NULL rows WITH a payloads row (legacy_rewrite), none here.
	wantExported := len(c.before) + created
	wantSkipped := 0

	dry := runCpexport(t, bin, "-root", root, "-db", e.dbPath, "-dry-run")
	require.Equal(t, 0, dry.exitCode, "stderr: %s", dry.stderr)
	require.True(t, dry.dryRun)
	require.Equal(t, wantExported, dry.exported, dry.stdout)
	require.Equal(t, wantSkipped, dry.legacySkipped, dry.stdout)
	require.Equal(t, 0, dry.undecodable, dry.stdout)
	filesAfterDry, _ := treeSize(t, root)
	require.Equal(t, filesBefore, filesAfterDry, "dry-run wrote nothing")

	start := time.Now()
	real := runCpexport(t, bin, "-root", root, "-db", e.dbPath)
	elapsed := time.Since(start)
	require.Equal(t, 0, real.exitCode, "stderr: %s", real.stderr)
	require.False(t, real.dryRun)
	require.Equal(t, dry.exported, real.exported, "the dry run predicted the export")
	require.Equal(t, dry.legacySkipped, real.legacySkipped)
	require.Equal(t, 0, real.undecodable)
	filesAfter, bytesAfter := treeSize(t, root)
	require.Equal(t, filesBefore+created, filesAfter, "exactly the store-created keys gained a file; no staging file left behind")
	t.Logf("export: %d files in %s (binary wall); files %d→%d (%dMB→%dMB), db+wal %dMB→%dMB; during the rollback window the %dMB of files duplicate the rows' payloads (total on disk %dMB)",
		real.exported, elapsed, filesBefore, filesAfter, bytesBefore>>20, bytesAfter>>20, dbBefore>>20, dbSize(t, e.dbPath)>>20, bytesAfter>>20, (bytesAfter+dbSize(t, e.dbPath))>>20)

	// Every exported file decodes to what the ObjectStore serves, at the
	// row's resourceVersion and UID, and the old binary reads it.
	e.startNew()
	verifyStart := time.Now()
	decoded := 0
	for k := range c.before {
		want := e.mustStoreGet(k)
		got := decodeGob(t, e.readFile(filepath.Base(k)))
		require.Equal(t, canonicalCP(want), canonicalCP(got), "%s: the exported file is not what the store serves", k)
		require.Equal(t, want.ResourceVersion, got.ResourceVersion, k)
		require.Equal(t, want.UID, got.UID, k)
		decoded++
	}
	for i := 0; i < created; i++ {
		k := e.key(fmt.Sprintf("created-under-the-new-store-%03d", i))
		want := e.mustStoreGet(k)
		got := decodeGob(t, e.readFile(filepath.Base(k)))
		require.Equal(t, canonicalCP(want), canonicalCP(got), k)
		decoded++
	}
	require.Equal(t, real.exported, decoded, "every exported file was decoded")
	e.stopNew()
	// The old binary serves a sample at the post-flip state.
	for _, name := range []string{"plain-00000", "plain-00299", "created-under-the-new-store-199", "diverged-042", "trap-003.g.tail"} {
		got := e.legacyGet(e.key(name))
		require.Equal(t, name, got.Name)
	}
	t.Logf("verified %d exported files in %s", decoded, time.Since(verifyStart))
}
