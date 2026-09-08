package file

// objectStoreCPStorage implements ContainerProfileStorage on top of the
// ObjectStore (design §3.6, §3.7). The connection-in-context convention of the
// legacy implementation is replaced by a read handle: WithConnection hands the
// consolidation pass one autocommit pool connection for its Phase 1 reads,
// BeginTransaction opens a STAGED WRITE SET on that handle, every write called
// while the set is open is appended to it as prepared SQL (with the CAS
// predicates captured from the same reads that produced the objects), and the
// end function returned by BeginTransaction executes the whole set under the
// gate in one BEGIN IMMEDIATE … COMMIT. Any CAS that matches no row (the
// base's, or a processed TS object's — R4) rolls the whole tick back and the
// end function reports ErrWriteConflict; the processor retries once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type readHandleKey struct{}

// rowVersion is the (rv, uid) a payload was read with: a CAS expectation.
type rowVersion struct {
	rv  int64
	uid string
}

// readHandle is what WithConnection puts in the context: one pool connection
// for autocommit reads, the versions of every object read through it, and the
// write set BeginTransaction opened (nil outside a transaction).
type readHandle struct {
	store *ObjectStore
	conn  *sqlite.Conn
	seen  map[string]rowVersion
	ws    *writeSet
}

func withReadHandle(ctx context.Context, h *readHandle) context.Context {
	return context.WithValue(ctx, readHandleKey{}, h)
}

func readHandleFrom(ctx context.Context) *readHandle {
	h, _ := ctx.Value(readHandleKey{}).(*readHandle)
	return h
}

func (h *readHandle) record(key string, rv int64, uid string) {
	if h.seen == nil {
		h.seen = make(map[string]rowVersion)
	}
	h.seen[key] = rowVersion{rv: rv, uid: uid}
}

// stagedStmt is one prepared statement group of a write set. run contains
// only sqlitex.Execute calls on already-bound values (INV-1).
type stagedStmt struct {
	name string
	run  func(conn *sqlite.Conn) error
}

// writeSet is the consolidation pass's Phase 2 payload: statements executed
// in order under the gate, and the Phase 3 events dispatched after COMMIT.
type writeSet struct {
	stmts []stagedStmt
	post  []func()
}

func (ws *writeSet) stage(name string, run func(conn *sqlite.Conn) error) {
	ws.stmts = append(ws.stmts, stagedStmt{name: name, run: run})
}

type objectStoreCPStorage struct {
	s    *ObjectStore
	sbom storage.Interface
}

var _ ContainerProfileStorage = (*objectStoreCPStorage)(nil)
var _ TimeSeriesEntryWriter = (*objectStoreCPStorage)(nil)
var _ ProcessedDeleteStager = (*objectStoreCPStorage)(nil)

func newObjectStoreCPStorage(s *ObjectStore, sbom storage.Interface) *objectStoreCPStorage {
	return &objectStoreCPStorage{s: s, sbom: sbom}
}

// StagesProcessedDeletes tells the processor to hand the processed-TS deletes
// to the store BEFORE the end function commits, so they join the tick's
// transaction (§3.7) instead of running after it.
func (c *objectStoreCPStorage) StagesProcessedDeletes() bool { return true }

// withConn runs fn on the context's read handle connection, or on a pool
// connection taken for the call.
func (c *objectStoreCPStorage) withConn(ctx context.Context, op, key string, fn func(conn *sqlite.Conn) error) error {
	if h := readHandleFrom(ctx); h != nil {
		return fn(h.conn)
	}
	conn, err := c.s.takeConn(ctx, op, key)
	if err != nil {
		return err
	}
	defer c.s.pool.Put(conn)
	return fn(conn)
}

// ---- TransactionManager ----

func (c *objectStoreCPStorage) WithConnection(ctx context.Context) (context.Context, func(), error) {
	if c.s.hooks.onPoolTake != nil {
		c.s.hooks.onPoolTake()
	}
	beforePool := time.Now()
	conn, err := c.s.pool.Take(ctx)
	if err != nil {
		metrics.ObservePoolWait(ContainerProfileKindPlural, metrics.OutcomeTimeout, time.Since(beforePool))
		return nil, nil, fmt.Errorf("failed to take connection from pool: %w", err)
	}
	metrics.ObservePoolWait(ContainerProfileKindPlural, metrics.OutcomeAcquired, time.Since(beforePool))
	h := &readHandle{store: c.s, conn: conn}
	var cleaned bool
	cleanup := func() {
		if !cleaned {
			cleaned = true
			c.s.pool.Put(conn)
		}
	}
	return withReadHandle(ctx, h), cleanup, nil
}

// BeginTransaction opens the write set. The returned function commits it
// under the gate when *err is nil (setting *err to ErrWriteConflict when any
// CAS in the set matched no row), and discards it otherwise.
func (c *objectStoreCPStorage) BeginTransaction(ctx context.Context) (func(*error), error) {
	h := readHandleFrom(ctx)
	if h == nil {
		return nil, errors.New("BeginTransaction: no connection in context (call WithConnection first)")
	}
	if h.ws != nil {
		return nil, errors.New("BeginTransaction: a write set is already open on this connection")
	}
	ws := &writeSet{}
	h.ws = ws
	return func(errp *error) {
		h.ws = nil
		if *errp != nil || len(ws.stmts) == 0 {
			return
		}
		err := c.s.gate.run(ctx, priorityLow, holdPathConsolidate, func(conn *sqlite.Conn) error {
			hook := c.s.stmtHook(conn, holdPathConsolidate)
			for _, st := range ws.stmts {
				if err := hook(st.name); err != nil {
					return err
				}
				if err := st.run(conn); err != nil {
					return err
				}
			}
			return hook("commit")
		})
		if err != nil {
			*errp = err
			return
		}
		c.s.checkpointer.afterCommit()
		for _, p := range ws.post {
			p()
		}
	}, nil
}

// ---- reads ----

func (c *objectStoreCPStorage) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	return c.getFull(ctx, key)
}

// GetTsContainerProfile is the same read as GetContainerProfile: there is no
// per-key lock to bypass.
func (c *objectStoreCPStorage) GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	return c.getFull(ctx, key)
}

func (c *objectStoreCPStorage) getFull(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	profile := softwarecomposition.ContainerProfile{}
	err := c.withConn(ctx, "get", key, func(conn *sqlite.Conn) error {
		row, err := c.s.getWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
		if err != nil {
			return err
		}
		if h := readHandleFrom(ctx); h != nil && row != nil {
			h.record(key, row.rv, row.uid)
		}
		return nil
	})
	return profile, err
}

func (c *objectStoreCPStorage) GetContainerProfileMetadata(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	profile := softwarecomposition.ContainerProfile{}
	err := c.withConn(ctx, "get", key, func(conn *sqlite.Conn) error {
		_, err := c.s.getWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile)
		return err
	})
	return profile, err
}

// GetContainerProfileMetadataNoLock is GetContainerProfileMetadata: the new
// store has no per-key lock, so there is nothing to skip.
func (c *objectStoreCPStorage) GetContainerProfileMetadataNoLock(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	return c.GetContainerProfileMetadata(ctx, key)
}

// GetSbom reads the sbomsyft kind through the legacy StorageImpl that owns it
// (R7): its per-key lock map is the one the SBOM writer uses. It runs in the
// prepare phase, never under the gate. When the caller already holds a read
// handle (every PreSave does), the read reuses that connection through
// GetWithConn instead of taking a second one: a nested pool acquisition under
// a full pool spins until the caller's deadline (measured in Tier B as 5 s
// stalls on ticks and updates before this reuse).
func (c *objectStoreCPStorage) GetSbom(ctx context.Context, key string) (softwarecomposition.SBOMSyft, error) {
	sbom := softwarecomposition.SBOMSyft{}
	if c.sbom == nil {
		return sbom, storage.NewKeyNotFoundError(key, 0)
	}
	if h := readHandleFrom(ctx); h != nil {
		if legacy, ok := c.sbom.(*StorageImpl); ok {
			return sbom, legacy.GetWithConn(ctx, h.conn, key, storage.GetOptions{}, &sbom)
		}
	}
	err := c.sbom.Get(ctx, key, storage.GetOptions{}, &sbom)
	return sbom, err
}

// ---- writes ----

// SaveContainerProfile creates or updates the base profile. Inside a write set
// the CAS UPDATE is staged with the expectation taken from the row the profile
// was read from (profile.ResourceVersion / profile.UID come from that read);
// outside one it is a gated GuaranteedUpdate on the low lane.
func (c *objectStoreCPStorage) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return profile, nil, nil
	}
	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	h := readHandleFrom(ctx)
	if h == nil || h.ws == nil {
		if err := c.s.guaranteedUpdate(cpCtx, key, &softwarecomposition.ContainerProfile{}, true, nil, tryUpdate, nil, "", priorityLow); err != nil {
			return fmt.Errorf("failed to update container profile: %w", err)
		}
		return nil
	}

	// Staged: prepare against a fresh autocommit read (the #315 DeepEqual
	// short-circuit compares against what is persisted), but the CAS
	// expectation is the profile's own (rv, uid) from the Phase 1 read.
	phase1RV, err := c.s.versioner.ObjectResourceVersion(profile)
	if err != nil {
		return fmt.Errorf("failed to read container profile resource version: %w", err)
	}
	var out softwarecomposition.ContainerProfile
	v, _ := conversion.EnforcePtr(&out)
	orig, err := c.s.readState(cpCtx, h.conn, key, true, v)
	if err != nil {
		return fmt.Errorf("failed to read container profile: %w", err)
	}
	if annotations := orig.obj.(*softwarecomposition.ContainerProfile).Annotations; annotations != nil && annotations[helpersv1.StatusMetadataKey] == helpersv1.TooLarge {
		return nil
	}
	pw, err := c.s.prepareUpdate(cpCtx, h, key, true, nil, tryUpdate, orig, true, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
	}
	if pw == nil {
		return nil
	}
	// Expect exactly the row Phase 1 read the profile from; a profile
	// synthesised for an absent base (RV 0) is a create-or-conflict INSERT.
	pw.insert = phase1RV == 0
	pw.expectRV = int64(phase1RV)
	pw.expectUID = pw.uid
	h.ws.stage("save-base", func(conn *sqlite.Conn) error {
		return c.s.execUpdate(conn, pw, noHook)
	})
	h.ws.post = append(h.ws.post, func() {
		c.s.watchDispatcher.Modified(key, pw.metaObj, pw.candidate)
	})
	return nil
}

// DeleteContainerProfile deletes key. Inside a write set the DELETE carries
// the (rv, uid) the object was read with on this handle (R4) and is staged;
// outside one it is a gated transaction of its own.
func (c *objectStoreCPStorage) DeleteContainerProfile(ctx context.Context, key string) error {
	h := readHandleFrom(ctx)
	if h == nil || h.ws == nil {
		return c.s.deleteKey(ctx, key, &softwarecomposition.ContainerProfile{}, nil, priorityLow)
	}
	var expect *rowVersion
	if rv, ok := h.seen[key]; ok {
		expect = &rv
	}
	res := &deleteResult{}
	h.ws.stage("delete-ts", func(conn *sqlite.Conn) error {
		return c.s.execDelete(conn, key, expect, res, noHook)
	})
	h.ws.post = append(h.ws.post, func() {
		metaOut := &softwarecomposition.ContainerProfile{}
		_ = json.Unmarshal(res.metadataJSON, metaOut)
		c.s.watchDispatcher.Deleted(key, metaOut)
	})
	return nil
}

// ---- TimeSeriesOperations ----

func (c *objectStoreCPStorage) ListTimeSeriesExpired(ctx context.Context, threshold time.Duration) (keys []string, err error) {
	err = c.withConn(ctx, "list", "", func(conn *sqlite.Conn) error {
		keys, err = ListTimeSeriesExpired(conn, threshold)
		return err
	})
	return keys, err
}

func (c *objectStoreCPStorage) ListTimeSeriesWithData(ctx context.Context) (keys []string, err error) {
	err = c.withConn(ctx, "list", "", func(conn *sqlite.Conn) error {
		keys, err = ListTimeSeriesWithData(conn)
		return err
	})
	return keys, err
}

func (c *objectStoreCPStorage) ListTimeSeriesContainers(ctx context.Context, key string) (out map[string][]softwarecomposition.TimeSeriesContainers, err error) {
	err = c.withConn(ctx, "list", key, func(conn *sqlite.Conn) error {
		out, err = ListTimeSeriesContainers(conn, key)
		return err
	})
	return out, err
}

func (c *objectStoreCPStorage) DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error {
	run := func(conn *sqlite.Conn) error { return DeleteTimeSeriesContainerEntries(conn, key) }
	if h := readHandleFrom(ctx); h != nil && h.ws != nil {
		h.ws.stage("delete-time-series", run)
		return nil
	}
	return c.s.gate.run(ctx, priorityLow, holdPathTimeSeries, run)
}

// ReplaceTimeSeriesContainerEntries stages (or runs) the per-series DELETE +
// INSERTs of ReplaceTimeSeriesContainerEntries with the suffix list marshalled
// at staging time, so nothing is encoded under the gate.
func (c *objectStoreCPStorage) ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	tsSuffixes, err := json.Marshal(deleteTimeSeries)
	if err != nil {
		return fmt.Errorf("failed to marshal tsSuffixes: %w", err)
	}
	rows := make([]TimeSeriesRow, 0, len(newTimeSeries))
	for _, p := range newTimeSeries {
		rows = append(rows, TimeSeriesRow{Kind: kind, Namespace: namespace, Name: name, SeriesID: seriesID, TsSuffix: p.TsSuffix,
			ReportTimestamp: p.ReportTimestamp, Status: p.Status, Completion: p.Completion, PreviousReportTimestamp: p.PreviousReportTimestamp, HasData: p.HasData})
	}
	run := func(conn *sqlite.Conn) error {
		if err := sqlitex.Execute(conn,
			`DELETE FROM time_series
				WHERE kind = ? AND namespace = ? AND name = ? AND seriesID = ?
					AND tsSuffix IN (SELECT value FROM json_each(?))`,
			&sqlitex.ExecOptions{Args: []any{kind, namespace, name, seriesID, string(tsSuffixes)}}); err != nil {
			return fmt.Errorf("delete time series entries: %w", err)
		}
		for i := range rows {
			if err := execTimeSeriesRow(conn, &rows[i]); err != nil {
				return fmt.Errorf("insert profile: %w", err)
			}
		}
		return nil
	}
	if h := readHandleFrom(ctx); h != nil && h.ws != nil {
		h.ws.stage("replace-time-series", run)
		return nil
	}
	return c.s.gate.run(ctx, priorityLow, holdPathTimeSeries, run)
}

// WriteTimeSeriesEntry is the AfterCreate fallback (a processor without
// TimeSeriesRowProvider); the ObjectStore's own Create writes the row inside
// the object's transaction and never calls this.
func (c *objectStoreCPStorage) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	row := TimeSeriesRow{Kind: kind, Namespace: namespace, Name: name, SeriesID: seriesID, TsSuffix: tsSuffix,
		ReportTimestamp: reportTimestamp, Status: status, Completion: completion, PreviousReportTimestamp: previousReportTimestamp, HasData: hasData}
	run := func(conn *sqlite.Conn) error { return execTimeSeriesRow(conn, &row) }
	if h := readHandleFrom(ctx); h != nil && h.ws != nil {
		h.ws.stage("write-time-series", run)
		return nil
	}
	return c.s.gate.run(ctx, priorityLow, holdPathTimeSeries, run)
}
