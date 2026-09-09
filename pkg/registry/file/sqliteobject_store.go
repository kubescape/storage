package file

// ObjectStore: the SQLite-native, fully-ACID storage.Interface for the
// ContainerProfile kind (design: .omc/plans/full-acid-storage-architecture.md,
// §3). The object's metadata row (with real rv/uid columns), its payload BLOB
// (versioned JSON) and its time_series row are written in ONE BEGIN IMMEDIATE …
// COMMIT on the gate's dedicated connection; the compare-and-swap is one UPDATE
// … WHERE rv=:rv AND uid=:uid. Nothing but SQL on already-prepared bytes runs
// while the gate is held (INV-1): no lock, no callback, no dispatch, no
// decode, no encode.
//
// PROTOTYPE (§7.6): no data migration, no cleanup.go / GeneratedNetworkPolicy
// re-pointing, no gate sharing with the 13 legacy kinds. Working package name
// in the design is sqliteobject; it lives in package file so the in-package
// measurement harness (containerprofile_load_test.go) can select it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/metrics"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// PayloadEncodingJSONV1Beta1 is the payloads.encoding value for a body that is
// the object converted to the v1beta1 storage version and JSON-marshalled.
const PayloadEncodingJSONV1Beta1 = "json/v1beta1"

// Write-path labels for storage_sqlite_write_hold_seconds.
const (
	holdPathCreate      = "create"
	holdPathUpdate      = "update"
	holdPathDelete      = "delete"
	holdPathConsolidate = "consolidate"
	holdPathTimeSeries  = "time_series"
)

// ObjectStoreOptions tunes an ObjectStore.
type ObjectStoreOptions struct {
	// CheckpointThresholdBytes is the -wal size that triggers a background
	// checkpoint after a gated commit; non-positive = DefaultCheckpointThresholdBytes.
	CheckpointThresholdBytes int64
	// CheckpointInterval is the timer fallback; non-positive = DefaultCheckpointInterval.
	CheckpointInterval time.Duration
}

// objectStoreHooks are test seams. Every field is nil in production.
type objectStoreHooks struct {
	// beforeStatement runs inside the gated transaction before staged
	// statement idx of a write set (crash-injection tests return an error or
	// panic from it, or roll the transaction back underneath the holder).
	beforeStatement func(conn *sqlite.Conn, path string, idx int, name string) error
	// afterPrepare runs after a write's prepare phase, before its gate ticket.
	afterPrepare func(path, key string)
	// onPoolTake runs on every pool connection acquisition by the store or its
	// ContainerProfileStorage (INV-1's "no pool wait inside the hold" probe).
	onPoolTake func()
}

// ObjectStore implements storage.Interface over the metadata + payloads +
// time_series tables. See the file comment.
type ObjectStore struct {
	pool            *sqlitemigration.Pool
	scheme          *runtime.Scheme
	versioner       storage.Versioner
	processor       Processor
	watchDispatcher eventDispatcher
	gate            *writeGate
	checkpointer    *checkpointer
	hooks           objectStoreHooks
}

// eventDispatcher is the slice of WatchDispatcher the store uses; an interface
// so INV-1's test can wrap it and prove no dispatch happens under the gate.
type eventDispatcher interface {
	Added(key string, metaOut, obj runtime.Object)
	Modified(key string, metaOut, obj runtime.Object)
	Deleted(key string, metaOut runtime.Object)
}

var _ storage.Interface = (*ObjectStore)(nil)

// NewObjectStore builds the store over pool (the same pool/database file the
// legacy StorageImpl uses), takes the gate's dedicated connection, starts the
// checkpointer and hands the processor its ContainerProfileStorage. dbPath is
// the database file (for the -wal size check); sbomStore is the legacy
// StorageImpl through which GetSbom reads the sbomsyft kind (R7).
func NewObjectStore(pool *sqlitemigration.Pool, dbPath string, watchDispatcher *WatchDispatcher, scheme *runtime.Scheme, processor Processor, sbomStore storage.Interface, opts ObjectStoreOptions) (*ObjectStore, error) {
	if watchDispatcher == nil {
		watchDispatcher = NewWatchDispatcher()
	}
	if processor == nil {
		processor = DefaultProcessor{}
	}
	ctx, cancel := poolContext()
	defer cancel()
	gate, err := newWriteGate(ctx, pool)
	if err != nil {
		return nil, err
	}
	s := &ObjectStore{
		pool:            pool,
		scheme:          scheme,
		versioner:       storage.APIObjectVersioner{},
		processor:       processor,
		watchDispatcher: watchDispatcher,
		gate:            gate,
		checkpointer:    newCheckpointer(pool, dbPath, opts.CheckpointThresholdBytes, opts.CheckpointInterval),
	}
	s.checkpointer.start()
	processor.SetStorage(newObjectStoreCPStorage(s, sbomStore))
	return s, nil
}

// Close stops the checkpointer and returns the gate's connection to the pool.
// It must run before Pool.Close (K-5).
func (s *ObjectStore) Close() error {
	s.checkpointer.Stop()
	return s.gate.Close()
}

// ---- storage.Interface plumbing ----

func (s *ObjectStore) Versioner() storage.Versioner                          { return s.versioner }
func (s *ObjectStore) ReadinessCheck() error                                  { return nil }
func (s *ObjectStore) RequestWatchProgress(context.Context) error             { return nil }
func (s *ObjectStore) GetCurrentResourceVersion(context.Context) (uint64, error) { return 0, nil }
func (s *ObjectStore) EnableResourceSizeEstimation(storage.KeysFunc) error    { return nil }
func (s *ObjectStore) CompactRevision() int64                                 { return 0 }
func (s *ObjectStore) SetKeysFunc(storage.KeysFunc)                           {}
func (s *ObjectStore) Stats(context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (s *ObjectStore) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	nw := newWatcher(ctx, opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec)
	if wd, ok := s.watchDispatcher.(*WatchDispatcher); ok {
		wd.Register(key, nw)
	} else if r, ok := s.watchDispatcher.(interface{ Register(string, *watcher) }); ok {
		r.Register(key, nw)
	}
	return nw, nil
}

// takeConn takes a pool connection for a read or prepare phase.
func (s *ObjectStore) takeConn(ctx context.Context, op, key string) (*sqlite.Conn, error) {
	if s.hooks.onPoolTake != nil {
		s.hooks.onPoolTake()
	}
	poolCtx, cancel := poolContext()
	defer cancel()
	before := time.Now()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeTimeout, time.Since(before))
		return nil, newContentionTimeoutError(op, key, err)
	}
	metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeAcquired, time.Since(before))
	conn.SetInterrupt(ctx.Done())
	return conn, nil
}

// ---- codec ----

// encodeBody converts obj to the v1beta1 storage version and marshals it. The
// original TypeMeta is kept (ConvertToVersion stamps the target GVK), so a
// decode returns the object with exactly the apiVersion/kind it was saved with.
func (s *ObjectStore) encodeBody(obj runtime.Object) ([]byte, error) {
	versioned, err := s.scheme.ConvertToVersion(obj, v1beta1.SchemeGroupVersion)
	if err != nil {
		return nil, fmt.Errorf("convert to v1beta1: %w", err)
	}
	versioned.GetObjectKind().SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())
	body, err := json.Marshal(versioned)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return body, nil
}

// decodeBody unmarshals a payloads.body into objPtr through the v1beta1 type.
func (s *ObjectStore) decodeBody(encoding string, body []byte, objPtr runtime.Object) error {
	if encoding != PayloadEncodingJSONV1Beta1 {
		return fmt.Errorf("unsupported payload encoding %q", encoding)
	}
	gvks, _, err := s.scheme.ObjectKinds(objPtr)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("object kinds: %w", err)
	}
	versioned, err := s.scheme.New(v1beta1.SchemeGroupVersion.WithKind(gvks[0].Kind))
	if err != nil {
		return fmt.Errorf("new v1beta1 %s: %w", gvks[0].Kind, err)
	}
	if err := json.Unmarshal(body, versioned); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if err := s.scheme.Convert(versioned, objPtr, nil); err != nil {
		return fmt.Errorf("convert from v1beta1: %w", err)
	}
	objPtr.GetObjectKind().SetGroupVersionKind(versioned.GetObjectKind().GroupVersionKind())
	return nil
}

// calculateChecksum is StorageImpl.CalculateChecksum without the receiver.
func (s *ObjectStore) calculateChecksum(in runtime.Object) (string, error) {
	return (&StorageImpl{scheme: s.scheme}).CalculateChecksum(in)
}

// ---- rows ----

// objRow is one (metadata JOIN payloads) row.
type objRow struct {
	metadataJSON []byte
	rv           int64
	uid          string
	encoding     string
	body         []byte
}

func (s *ObjectStore) readRow(conn *sqlite.Conn, key string) (*objRow, error) {
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	var row *objRow
	err := sqlitex.Execute(conn,
		`SELECT m.metadata, m.rv, m.uid, p.encoding, p.body
			FROM metadata m JOIN payloads p USING (kind, namespace, name)
			WHERE m.kind = :kind AND m.namespace = :namespace AND m.name = :name`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":name": name},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				r := &objRow{
					metadataJSON: []byte(stmt.ColumnText(0)),
					rv:           stmt.ColumnInt64(1),
					uid:          stmt.ColumnText(2),
					encoding:     stmt.ColumnText(3),
				}
				r.body = make([]byte, stmt.ColumnLen(4))
				stmt.ColumnBytes(4, r.body)
				row = r
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("read object row: %w", err)
	}
	return row, nil
}

// getWithConn is Get on a given connection. The metadata variant reads the
// shared metadata row exactly as the legacy store does; the full variant is
// one autocommit SELECT over the join (a WAL snapshot; no per-key lock).
func (s *ObjectStore) getWithConn(ctx context.Context, conn *sqlite.Conn, key string, opts storage.GetOptions, objPtr runtime.Object) (*objRow, error) {
	if opts.ResourceVersion == softwarecomposition.ResourceVersionMetadata {
		metadata, err := ReadMetadata(conn, key)
		if err != nil {
			if errors.Is(err, ErrMetadataNotFound) {
				if opts.IgnoreNotFound {
					return nil, runtime.SetZeroValue(objPtr)
				}
				return nil, storage.NewKeyNotFoundError(key, 0)
			}
			return nil, fmt.Errorf("read metadata: %w", err)
		}
		return nil, json.Unmarshal(metadata, objPtr)
	}
	row, err := s.readRow(conn, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		if opts.IgnoreNotFound {
			return nil, runtime.SetZeroValue(objPtr)
		}
		return nil, storage.NewKeyNotFoundError(key, 0)
	}
	if err := s.decodeBody(row.encoding, row.body, objPtr); err != nil {
		logger.L().Ctx(ctx).Error("ObjectStore.Get - decode payload failed", helpers.Error(err), helpers.String("key", key))
		return nil, err
	}
	return row, nil
}

// Get implements storage.Interface.
func (s *ObjectStore) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	conn, err := s.takeConn(ctx, "get", key)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	_, err = s.getWithConn(ctx, conn, key, opts, objPtr)
	return err
}

// GetList implements storage.Interface. The metadata variant is byte-identical
// to the legacy store's (same listMetadata statement, same continue token =
// rowid); the fullSpec variant is a paginated join, one statement per page.
func (s *ObjectStore) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	ctx, predicate, v, elem, limit, batchSize, cursor, isFullSpec, err := (&StorageImpl{}).prepareGetList(ctx, key, opts, listObj)
	if err != nil {
		return err
	}
	pageLast := ""
	for limit == 0 || int64(v.Len()) < limit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		remaining := nextPageSize(limit, batchSize, int64(v.Len()))
		conn, err := s.takeConn(ctx, "list", key)
		if err != nil {
			return err
		}
		fetched, err := s.fetchListPage(ctx, conn, key, cursor, remaining, isFullSpec, predicate, v, elem)
		s.pool.Put(conn)
		if err != nil {
			return err
		}
		pageLast = fetched.pageLast
		if int64(fetched.count) < remaining {
			pageLast = ""
			break
		}
		cursor = pageLast
	}
	return setListContinue(listObj, pageLast)
}

func (s *ObjectStore) fetchListPage(ctx context.Context, conn *sqlite.Conn, key, cursor string, remaining int64, isFullSpec bool, predicate storage.SelectionPredicate, v reflect.Value, elem reflect.Type) (listPageResult, error) {
	var objs []runtime.Object
	var pageLast string
	var count int
	if !isFullSpec {
		entries, last, err := listMetadata(conn, key, cursor, remaining)
		if err != nil {
			return listPageResult{}, fmt.Errorf("list objects for %q: %w", key, err)
		}
		pageLast, count = last, len(entries)
		for _, entry := range entries {
			obj := reflect.New(elem).Interface().(runtime.Object)
			if err := json.Unmarshal([]byte(entry), obj); err != nil {
				logger.L().Ctx(ctx).Error("ObjectStore.GetList - unmarshal metadata failed", helpers.Error(err), helpers.String("key", key))
				continue
			}
			objs = append(objs, obj)
		}
	} else {
		_, _, kind, _, namespace, _ := K8sPathToKeys(key)
		if cursor == "" {
			cursor = "0"
		}
		type page struct {
			encoding string
			body     []byte
		}
		var rows []page
		// The page is selected on metadata by rowid FIRST (the legacy
		// listMetadataKeys statement), then joined to payloads by primary key:
		// design PM-1's "apply the predicate before the join". A plain join
		// let SQLite drive from payloads and touch every body (Tier B: LIST
		// p95 6.8 -> 80 ms).
		err := sqlitex.Execute(conn,
			`SELECT m.rowid, p.encoding, p.body
				FROM (SELECT rowid, kind, namespace, name FROM metadata
						WHERE kind = :kind
							AND (:namespace = '' OR namespace = :namespace)
							AND rowid > :cont
						ORDER BY rowid
						LIMIT :limit) m
				JOIN payloads p ON p.kind = m.kind AND p.namespace = m.namespace AND p.name = m.name
				ORDER BY m.rowid`,
			&sqlitex.ExecOptions{
				Named: map[string]any{":kind": kind, ":namespace": namespace, ":cont": cursor, ":limit": remaining},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					pageLast = stmt.ColumnText(0)
					p := page{encoding: stmt.ColumnText(1), body: make([]byte, stmt.ColumnLen(2))}
					stmt.ColumnBytes(2, p.body)
					rows = append(rows, p)
					return nil
				},
			})
		if err != nil {
			return listPageResult{}, fmt.Errorf("list objects for %q: %w", key, err)
		}
		count = len(rows)
		for _, r := range rows {
			obj := reflect.New(elem).Interface().(runtime.Object)
			if err := s.decodeBody(r.encoding, r.body, obj); err != nil {
				logger.L().Ctx(ctx).Error("ObjectStore.GetList - decode payload failed", helpers.Error(err), helpers.String("key", key))
				continue
			}
			objs = append(objs, obj)
		}
	}
	v.Grow(len(objs))
	for _, obj := range objs {
		matched, err := predicate.Matches(obj)
		if err != nil {
			return listPageResult{}, fmt.Errorf("match selection predicate: %w", err)
		}
		if matched {
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
	}
	return listPageResult{pageLast: pageLast, count: count}, nil
}

// ---- prepared writes ----

// preparedWrite is one fully-prepared object write: bytes and predicates only,
// so the gate holder has nothing to compute.
type preparedWrite struct {
	key                   string
	kind, namespace, name string
	metadataJSON          []byte
	body                  []byte
	rv                    int64
	uid                   string
	// insert selects INSERT … ON CONFLICT DO NOTHING (create, or an
	// ignoreNotFound update of an absent key) instead of the CAS UPDATE.
	insert    bool
	expectRV  int64
	expectUID string
	// candidate is the object as persisted (RV bumped, checksum stamped);
	// metaObj is its ObjectMeta-only copy for the lightweight watch event.
	candidate runtime.Object
	metaObj   runtime.Object
	// tsRow / baseKey are set for a TS-profile create: the time_series row
	// that joins the transaction and the base key whose admission is
	// re-checked inside it.
	tsRow   *TimeSeriesRow
	baseKey string
	// tsCompletion is the incoming TS profile's completion annotation, for
	// the in-transaction admission rule.
	tsCompletion string
}

// stampAndEncode performs saveObject's pre-encode pipeline (RV bump,
// ManagedFields zeroing, checksum annotation) on obj and encodes it.
func (s *ObjectStore) stampAndEncode(key string, obj runtime.Object, checksum string) (*preparedWrite, error) {
	version, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		return nil, fmt.Errorf("object resource version: %w", err)
	}
	if err := s.versioner.UpdateObject(obj, version+1); err != nil {
		return nil, fmt.Errorf("set resourceVersion: %w", err)
	}
	managedFields := reflect.ValueOf(obj).Elem().FieldByName("ObjectMeta").FieldByName("ManagedFields")
	if managedFields.IsValid() {
		managedFields.Set(reflect.Zero(managedFields.Type()))
	}
	if checksum == "" {
		checksum, err = s.calculateChecksum(obj)
		if err != nil {
			return nil, fmt.Errorf("calculate checksum: %w", err)
		}
	}
	if anno := obj.(metav1.Object).GetAnnotations(); anno == nil {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.SyncChecksumMetadataKey: checksum})
	} else {
		anno[helpersv1.SyncChecksumMetadataKey] = checksum
	}
	metaObj := extractFields(obj, []string{"ObjectMeta", "SchemaVersion"})
	metadataJSON, err := json.Marshal(metaObj)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	body, err := s.encodeBody(obj)
	if err != nil {
		return nil, err
	}
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	return &preparedWrite{
		key: key, kind: kind, namespace: namespace, name: name,
		metadataJSON: metadataJSON,
		body:         body,
		rv:           int64(version + 1),
		uid:          string(obj.(metav1.Object).GetUID()),
		expectRV:     int64(version),
		expectUID:    string(obj.(metav1.Object).GetUID()),
		candidate:    obj,
		metaObj:      metaObj,
	}, nil
}

// fillOut copies the persisted object into the REST layer's out parameter,
// exactly as saveObject does (shallow struct copy).
func fillOut(metaOut, candidate runtime.Object) {
	if metaOut == nil {
		return
	}
	val := reflect.ValueOf(metaOut)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	val.Set(reflect.ValueOf(candidate).Elem())
}

// applyTooLarge is the shared ObjectTooLargeError handling of Create and
// GuaranteedUpdate: clear the spec and mark the object, then save it anyway.
func applyTooLarge(obj runtime.Object) {
	clearSpec(obj)
	metadata := obj.(metav1.Object)
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
	metadata.SetAnnotations(annotations)
}

// ---- the three transactions (§3.4), as statement lists ----

// statusPath / completionPath are json_extract paths into the metadata JSON.
// The internal ContainerProfile embeds ObjectMeta without a json tag, so
// encoding/json inlines its fields at the top level ("$.annotations", not
// "$.metadata.annotations"); the in-transaction admission check is therefore
// SQL and a string compare, not a Go decode (INV-1). The legacy store binds
// the JSON as a BLOB (json_extract would read it as JSONB and return NULL),
// hence the CAST; this store binds TEXT.
var (
	statusPath     = fmt.Sprintf(`$.annotations."%s"`, helpersv1.StatusMetadataKey)
	completionPath = fmt.Sprintf(`$.annotations."%s"`, helpersv1.CompletionMetadataKey)
)

// execCreate runs Create's transaction body on conn.
func (s *ObjectStore) execCreate(conn *sqlite.Conn, pw *preparedWrite, hook func(string) error) error {
	if pw.tsRow != nil && pw.baseKey != "" {
		if err := hook("ts-admission"); err != nil {
			return err
		}
		_, _, bkind, _, bns, bname := K8sPathToKeys(pw.baseKey)
		var status, completion string
		var found bool
		err := sqlitex.Execute(conn,
			`SELECT json_extract(CAST(metadata AS TEXT), :statusPath), json_extract(CAST(metadata AS TEXT), :completionPath)
				FROM metadata WHERE kind = :kind AND namespace = :namespace AND name = :name`,
			&sqlitex.ExecOptions{
				Named: map[string]any{":statusPath": statusPath, ":completionPath": completionPath, ":kind": bkind, ":namespace": bns, ":name": bname},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					found = true
					status = stmt.ColumnText(0)
					completion = stmt.ColumnText(1)
					return nil
				},
			})
		if err != nil {
			return fmt.Errorf("ts admission read: %w", err)
		}
		if found {
			switch {
			case status == helpersv1.TooLarge:
				return ObjectTooLargeError
			case status == helpersv1.Completed && (completion == helpersv1.Full || pw.tsCompletion == helpersv1.Partial):
				return ObjectCompletedError
			}
		}
	}
	if err := hook("insert-metadata"); err != nil {
		return err
	}
	if err := sqlitex.Execute(conn,
		`INSERT INTO metadata (kind, namespace, name, metadata, rv, uid)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		&sqlitex.ExecOptions{Args: []any{pw.kind, pw.namespace, pw.name, string(pw.metadataJSON), pw.rv, pw.uid}}); err != nil {
		return fmt.Errorf("insert metadata: %w", err)
	}
	if conn.Changes() == 0 {
		return storage.NewKeyExistsError(pw.key, 0)
	}
	if err := hook("insert-payload"); err != nil {
		return err
	}
	if err := sqlitex.Execute(conn,
		`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{pw.kind, pw.namespace, pw.name, PayloadEncodingJSONV1Beta1, pw.body}}); err != nil {
		// A UNIQUE failure here is an orphan payloads row (K-2's shape).
		return apierrors.NewInternalError(fmt.Errorf("insert payload for %q: %w", pw.key, err))
	}
	if pw.tsRow != nil {
		if err := hook("insert-time-series"); err != nil {
			return err
		}
		if err := execTimeSeriesRow(conn, pw.tsRow); err != nil {
			return err
		}
	}
	return hook("commit")
}

func execTimeSeriesRow(conn *sqlite.Conn, r *TimeSeriesRow) error {
	return WriteTimeSeriesEntry(conn, r.Kind, r.Namespace, r.Name, r.SeriesID, r.TsSuffix, r.ReportTimestamp, r.Status, r.Completion, r.PreviousReportTimestamp, r.HasData)
}

// execUpdate runs GuaranteedUpdate's transaction body on conn: the CAS UPDATE
// (or the create-or-conflict INSERT for an absent key), then the payloads
// UPDATE whose changes()==1 is asserted (K-6).
func (s *ObjectStore) execUpdate(conn *sqlite.Conn, pw *preparedWrite, hook func(string) error) error {
	if pw.insert {
		if err := hook("insert-metadata"); err != nil {
			return err
		}
		if err := sqlitex.Execute(conn,
			`INSERT INTO metadata (kind, namespace, name, metadata, rv, uid)
				VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			&sqlitex.ExecOptions{Args: []any{pw.kind, pw.namespace, pw.name, string(pw.metadataJSON), pw.rv, pw.uid}}); err != nil {
			return fmt.Errorf("insert metadata: %w", err)
		}
		if conn.Changes() == 0 {
			metrics.IncCPCASConflict("update")
			return errWriteConflict
		}
		if err := hook("insert-payload"); err != nil {
			return err
		}
		if err := sqlitex.Execute(conn,
			`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{pw.kind, pw.namespace, pw.name, PayloadEncodingJSONV1Beta1, pw.body}}); err != nil {
			return apierrors.NewInternalError(fmt.Errorf("insert payload for %q: %w", pw.key, err))
		}
		return hook("commit")
	}
	if err := hook("update-metadata"); err != nil {
		return err
	}
	if err := sqlitex.Execute(conn,
		`UPDATE metadata SET metadata = ?, rv = ?, uid = ?
			WHERE kind = ? AND namespace = ? AND name = ? AND rv = ? AND uid = ?`,
		&sqlitex.ExecOptions{Args: []any{string(pw.metadataJSON), pw.rv, pw.uid, pw.kind, pw.namespace, pw.name, pw.expectRV, pw.expectUID}}); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}
	if conn.Changes() == 0 {
		metrics.IncCPCASConflict("update")
		return errWriteConflict
	}
	if err := hook("update-payload"); err != nil {
		return err
	}
	if err := sqlitex.Execute(conn,
		`UPDATE payloads SET encoding = ?, body = ? WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{PayloadEncodingJSONV1Beta1, pw.body, pw.kind, pw.namespace, pw.name}}); err != nil {
		return fmt.Errorf("update payload: %w", err)
	}
	if n := conn.Changes(); n != 1 {
		return apierrors.NewInternalError(fmt.Errorf("ObjectStore: metadata row for %q has %d payloads rows (INV-2 violated); transaction rolled back", pw.key, n))
	}
	return hook("commit")
}

// deleteResult carries the deleted metadata JSON out of the transaction so
// the decode into metaOut happens after COMMIT.
type deleteResult struct {
	metadataJSON []byte
	found        bool
}

// execDelete runs Delete's transaction body on conn. When expect is non-nil
// the metadata DELETE carries the per-object CAS (R4).
func (s *ObjectStore) execDelete(conn *sqlite.Conn, key string, expect *rowVersion, out *deleteResult, hook func(string) error) error {
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	if err := hook("delete-metadata"); err != nil {
		return err
	}
	query := `DELETE FROM metadata WHERE kind = :kind AND namespace = :namespace AND name = :name RETURNING metadata`
	named := map[string]any{":kind": kind, ":namespace": namespace, ":name": name}
	if expect != nil {
		query = `DELETE FROM metadata WHERE kind = :kind AND namespace = :namespace AND name = :name AND rv = :rv AND uid = :uid RETURNING metadata`
		named[":rv"] = expect.rv
		named[":uid"] = expect.uid
	}
	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{
		Named: named,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out.found = true
			out.metadataJSON = []byte(stmt.ColumnText(0))
			return nil
		},
	}); err != nil {
		return fmt.Errorf("delete metadata: %w", err)
	}
	if !out.found {
		if expect != nil {
			// Distinguish "gone" from "changed under us": a row that still
			// exists at another version is the R4 conflict.
			var exists bool
			if err := sqlitex.Execute(conn,
				`SELECT 1 FROM metadata WHERE kind = :kind AND namespace = :namespace AND name = :name`,
				&sqlitex.ExecOptions{
					Named:      map[string]any{":kind": kind, ":namespace": namespace, ":name": name},
					ResultFunc: func(*sqlite.Stmt) error { exists = true; return nil },
				}); err != nil {
				return fmt.Errorf("delete metadata re-check: %w", err)
			}
			if exists {
				metrics.IncCPCASConflict("delete")
				return errWriteConflict
			}
		}
		return storage.NewKeyNotFoundError(key, 0)
	}
	if err := hook("delete-payload"); err != nil {
		return err
	}
	if err := sqlitex.Execute(conn,
		`DELETE FROM payloads WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{kind, namespace, name}}); err != nil {
		return fmt.Errorf("delete payload: %w", err)
	}
	if IsContainerProfileKind(kind) {
		if err := hook("delete-time-series"); err != nil {
			return err
		}
		if err := DeleteTimeSeriesContainerEntries(conn, key); err != nil {
			return err
		}
	}
	return hook("commit")
}

// ---- Create ----

// Create implements storage.Interface (§3.4 Create).
func (s *ObjectStore) Create(ctx context.Context, key string, obj, metaOut runtime.Object, _ uint64) error {
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		msg := "resourceVersion should not be set on objects to be created"
		logger.L().Ctx(ctx).Error(msg)
		return errors.New(msg)
	}

	// prepare (caller goroutine, no transaction, one pool connection for reads)
	conn, err := s.takeConn(ctx, "create", key)
	if err != nil {
		return err
	}
	h := &readHandle{store: s, conn: conn}
	presaveCtx := withReadHandle(ctx, h)
	if err := s.processor.PreSave(presaveCtx, obj); err != nil {
		if errors.Is(err, ObjectTooLargeError) {
			applyTooLarge(obj)
			logger.L().Debug("Create - too large object, saving metadata only", helpers.String("key", key))
		} else {
			s.pool.Put(conn)
			return err
		}
	}
	s.pool.Put(conn)

	pw, err := s.stampAndEncode(key, obj, "")
	if err != nil {
		return err
	}
	provider, hasProvider := s.processor.(TimeSeriesRowProvider)
	if hasProvider {
		if row, baseKey, ok := provider.TimeSeriesRowFor(obj); ok {
			pw.tsRow = &row
			pw.baseKey = baseKey
			pw.tsCompletion = obj.(metav1.Object).GetAnnotations()[helpersv1.CompletionMetadataKey]
		}
	}
	if s.hooks.afterPrepare != nil {
		s.hooks.afterPrepare(holdPathCreate, key)
	}

	// transaction (gate held)
	err = s.gate.run(ctx, priorityHigh, holdPathCreate, func(conn *sqlite.Conn) error {
		return s.execCreate(conn, pw, s.stmtHook(conn, holdPathCreate))
	})
	if err != nil {
		if !storage.IsExist(err) && !errors.Is(err, ObjectCompletedError) && !errors.Is(err, ObjectTooLargeError) {
			logger.L().Ctx(ctx).Error("Create - save object failed", helpers.Error(err), helpers.String("key", key))
		}
		return err
	}
	s.checkpointer.afterCommit()

	// after (caller goroutine, gate released)
	if !hasProvider {
		conn2, err := s.takeConn(ctx, "create", key)
		if err != nil {
			return err
		}
		afterCtx := withReadHandle(ctx, &readHandle{store: s, conn: conn2})
		err = s.processor.AfterCreate(afterCtx, pw.candidate)
		s.pool.Put(conn2)
		if err != nil {
			return fmt.Errorf("processor.AfterCreate: %w", err)
		}
	}
	fillOut(metaOut, pw.candidate)
	s.watchDispatcher.Added(key, pw.metaObj, pw.candidate)
	return nil
}

// stmtHook returns the per-transaction statement-boundary hook for path: a
// counter over the statements of ONE gated transaction, feeding the
// beforeStatement seam. Nil-cheap in production.
func (s *ObjectStore) stmtHook(conn *sqlite.Conn, path string) func(name string) error {
	if s.hooks.beforeStatement == nil {
		return noHook
	}
	idx := 0
	return func(name string) error {
		idx++
		return s.hooks.beforeStatement(conn, path, idx, name)
	}
}

func noHook(string) error { return nil }

// ---- GuaranteedUpdate ----

// updateState is one read of the current object with the row version the
// payload came from: the CAS expectation.
type updateState struct {
	obj    runtime.Object
	rev    int64
	uid    string
	exists bool
}

// readState reads key on conn into a fresh object of v's type.
func (s *ObjectStore) readState(ctx context.Context, conn *sqlite.Conn, key string, ignoreNotFound bool, v reflect.Value) (*updateState, error) {
	objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
	row, err := s.getWithConn(ctx, conn, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr)
	if err != nil {
		return nil, err
	}
	st := &updateState{obj: objPtr}
	if row != nil {
		st.exists = true
		st.rev = row.rv
		st.uid = row.uid
	}
	return st, nil
}

// prepareUpdate is guaranteedUpdateSingleWriter's prepare phase: preconditions,
// tryUpdate, PreSave on both sides, the #315 DeepEqual short-circuit, then
// stamp + encode with the CAS expectation taken from the row the payload came
// from. It returns (nil, state, nil) for a no-op update; (pw, state, nil) for a
// write to commit; the loop over conflicts belongs to the caller.
//
// Returned errors: retryable staleness is signalled by errStaleState.
var errStaleState = errors.New("stale state")

func (s *ObjectStore) prepareUpdate(ctx context.Context, h *readHandle, key string, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, origState *updateState, origStateIsCurrent bool, checksum string) (*preparedWrite, error) {

	if err := preconditions.Check(key, origState.obj); err != nil {
		if origStateIsCurrent {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - preconditions check failed", helpers.Error(err), helpers.String("key", key))
			return nil, err
		}
		return nil, errStaleState
	}

	orig := origState.obj.DeepCopyObject()
	presaveCtx := withReadHandle(ctx, h)
	_ = s.processor.PreSave(presaveCtx, orig)

	ret, _, err := tryUpdate(origState.obj, storage.ResponseMeta{})
	if err != nil {
		if origStateIsCurrent {
			if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
			}
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errStaleState, err)
	}

	if err := s.processor.PreSave(presaveCtx, ret); err != nil {
		if errors.Is(err, ObjectTooLargeError) {
			applyTooLarge(ret)
			logger.L().Debug("GuaranteedUpdate - too large object, skipping update", helpers.String("key", key))
		} else {
			logger.L().Debug("GuaranteedUpdate - processor.PreSave failed", helpers.Error(err), helpers.String("key", key))
			return nil, err
		}
	}

	if reflect.DeepEqual(orig, ret) {
		logger.L().Debug("GuaranteedUpdate - tryUpdate returned the same object, no update needed", helpers.String("key", key))
		return nil, nil
	}

	pw, err := s.stampAndEncode(key, ret, checksum)
	if err != nil {
		return nil, err
	}
	pw.expectRV = origState.rev
	pw.expectUID = origState.uid
	pw.insert = !origState.exists
	return pw, nil
}

// GuaranteedUpdate implements storage.Interface (§3.4 Update): read → prepare →
// one CAS transaction under the gate; on conflict, backoff, re-read, retry.
func (s *ObjectStore) GuaranteedUpdate(ctx context.Context, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return s.guaranteedUpdate(ctx, key, metaOut, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject, "", priorityHigh)
}

func (s *ObjectStore) guaranteedUpdate(ctx context.Context, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object,
	checksum string, priority writePriority) error {

	v, err := conversion.EnforcePtr(metaOut)
	if err != nil {
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	conn, err := s.takeConn(ctx, "update", key)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	h := &readHandle{store: s, conn: conn}

	var origState *updateState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		rv, err := s.versioner.ObjectResourceVersion(cachedExistingObject)
		if err != nil {
			return fmt.Errorf("couldn't get resource version: %v", err)
		}
		origState = &updateState{obj: cachedExistingObject, rev: int64(rv), uid: string(cachedExistingObject.(metav1.Object).GetUID()), exists: rv != 0}
	} else {
		origState, err = s.readState(ctx, conn, key, ignoreNotFound, v)
		if err != nil {
			return err
		}
		origStateIsCurrent = true
	}

	if annotations := origState.obj.(metav1.Object).GetAnnotations(); annotations != nil && annotations[helpersv1.StatusMetadataKey] == helpersv1.TooLarge {
		logger.L().Debug("GuaranteedUpdate - already too large object, skipping update", helpers.String("key", key))
		v.Set(reflect.ValueOf(origState.obj).Elem())
		return nil
	}

	var conflictAttempts int
	for {
		pw, err := s.prepareUpdate(ctx, h, key, ignoreNotFound, preconditions, tryUpdate, origState, origStateIsCurrent, checksum)
		if errors.Is(err, errStaleState) {
			origState, err = s.readState(ctx, conn, key, ignoreNotFound, v)
			if err != nil {
				return err
			}
			origStateIsCurrent = true
			continue
		}
		if err != nil {
			return err
		}
		if pw == nil {
			v.Set(reflect.ValueOf(origState.obj).Elem())
			return nil
		}
		if s.hooks.afterPrepare != nil {
			s.hooks.afterPrepare(holdPathUpdate, key)
		}

		err = s.gate.run(ctx, priority, holdPathUpdate, func(conn *sqlite.Conn) error {
			return s.execUpdate(conn, pw, s.stmtHook(conn, holdPathUpdate))
		})
		if errors.Is(err, errWriteConflict) {
			conflictAttempts++
			metrics.IncSingleWriterConflictRetry(resourceFromKey(key))
			if backoffErr := singleWriterConflictBackoff(ctx, conflictAttempts); backoffErr != nil {
				return newContentionTimeoutError("update", key, backoffErr)
			}
			origState, err = s.readState(ctx, conn, key, ignoreNotFound, v)
			if err != nil {
				return err
			}
			origStateIsCurrent = true
			continue
		}
		if err != nil {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - save object failed", helpers.Error(err), helpers.String("key", key))
			return err
		}
		s.checkpointer.afterCommit()
		fillOut(metaOut, pw.candidate)
		s.watchDispatcher.Modified(key, pw.metaObj, pw.candidate)
		return nil
	}
}

// ---- Delete ----

// Delete implements storage.Interface (§3.4 Delete): metadata + payloads +
// time_series rows removed in one transaction; KeyNotFound when no row.
func (s *ObjectStore) Delete(ctx context.Context, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	return s.deleteKey(ctx, key, metaOut, nil, priorityHigh)
}

func (s *ObjectStore) deleteKey(ctx context.Context, key string, metaOut runtime.Object, expect *rowVersion, priority writePriority) error {
	var res deleteResult
	err := s.gate.run(ctx, priority, holdPathDelete, func(conn *sqlite.Conn) error {
		return s.execDelete(conn, key, expect, &res, s.stmtHook(conn, holdPathDelete))
	})
	if err != nil {
		if !storage.IsNotFound(err) {
			logger.L().Ctx(ctx).Error("Delete - delete failed", helpers.Error(err), helpers.String("key", key))
		}
		return err
	}
	s.checkpointer.afterCommit()
	if metaOut != nil {
		if err := json.Unmarshal(res.metadataJSON, metaOut); err != nil {
			return fmt.Errorf("unmarshal deleted metadata: %w", err)
		}
	}
	s.watchDispatcher.Deleted(key, metaOut)
	return nil
}
