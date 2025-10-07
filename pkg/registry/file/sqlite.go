package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

var (
	ErrMetadataNotFound = errors.New("metadata not found")
)

// NewPool creates a new SQLite connection pool at the given path.
// It returns an error if the connection cannot be opened or the database cannot be initialized.
// It is your responsibility to call conn.Close() when you no longer need conn.
func NewPool(path string, size int) *sqlitemigration.Pool {
	return sqlitemigration.NewPool(path,
		sqlitemigration.Schema{
			Migrations: []string{
				`CREATE TABLE IF NOT EXISTS metadata (
					kind TEXT,
					namespace TEXT,
					name TEXT,
					metadata JSON,
					PRIMARY KEY (kind, namespace, name)
				);`,
				`ALTER TABLE metadata ADD COLUMN last_updated INTEGER;`,
				// trigger for setting last_updated on insert
				`CREATE TRIGGER insert_metadata_timestamp
					AFTER INSERT ON metadata
					FOR EACH ROW
					BEGIN
						UPDATE metadata 
						SET last_updated = unixepoch('now') 
						WHERE rowid = new.rowid;
					END;`,
				// trigger for updating last_updated on update
				`CREATE TRIGGER update_metadata_timestamp
					AFTER UPDATE ON metadata
					FOR EACH ROW
					BEGIN
						UPDATE metadata 
						SET last_updated = unixepoch('now') 
						WHERE rowid = old.rowid;
					END;`,
				`CREATE TABLE IF NOT EXISTS time_series (
    				kind TEXT,
					namespace TEXT,
					name TEXT,
					seriesID TEXT,
					reportTimestamp TEXT,
					status TEXT,
					tsSuffix TEXT,
					completion TEXT,
					previousReportTimestamp TEXT,
					hasData INTEGER DEFAULT 0,
					PRIMARY KEY (kind, namespace, name, seriesID, tsSuffix)
				);`,
			},
		},
		sqlitemigration.Options{
			PoolSize: size,
		})
}

// NewTestPool creates a new temporary SQLite connection (for testing only).
func NewTestPool(dir string) *sqlitemigration.Pool {
	path := filepath.Join(dir, "test.sq3")
	_ = os.Remove(path)
	return NewPool(path, 0)
}

func keysToPath(prefix, root, kind, ns, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", prefix, root, kind, ns, name)
}

func pathToKeys(path string) (string, string, string, string, string) {
	s := strings.SplitN(path, "/", 5)
	// ensure we have at least 5 parts
	for len(s) < 5 {
		s = append(s, "")
	}
	return s[0], s[1], s[2], s[3], s[4]
}

func countMetadata(conn *sqlite.Conn, path string) (int64, error) {
	_, _, kind, namespace, _ := pathToKeys(path)
	var count int64
	err := sqlitex.Execute(conn,
		`SELECT COUNT(*) FROM metadata
                WHERE kind = :kind
                  AND (:namespace = '' OR namespace = :namespace)`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt64(0)
				return nil
			},
		})
	if err != nil {
		return 0, fmt.Errorf("count metadata: %w", err)
	}
	return count, nil
}

// DeleteMetadata deletes metadata for the given path and unmarshals the deleted metadata into the provided runtime.Object.
func DeleteMetadata(conn *sqlite.Conn, path string, metadata runtime.Object) error {
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.Execute(conn,
		`DELETE FROM metadata
				WHERE kind = :kind
				  AND namespace = :namespace
				  AND name = :name
				RETURNING metadata`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":name": name},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				metadataJSON := stmt.ColumnText(0)
				if metadata == nil {
					return nil
				}
				return json.Unmarshal([]byte(metadataJSON), metadata)
			},
		})
	if err != nil {
		return fmt.Errorf("delete metadata: %w", err)
	}
	return nil
}

func listMetadataKeys(conn *sqlite.Conn, path, cont string, since, limit int64) ([]string, string, int64, error) {
	prefix, root, kind, namespace, _ := pathToKeys(path)
	if cont == "" {
		cont = "0"
	}
	var last string
	var lastUpdated int64
	var names []string
	err := sqlitex.Execute(conn,
		`SELECT rowid, namespace, name, last_updated FROM metadata
                WHERE kind = :kind
                    AND (:namespace = '' OR namespace = :namespace)
                	AND rowid > :cont
                	AND (:since = 0 OR last_updated > :since)
				ORDER BY rowid
				LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":cont": cont, ":since": since, ":limit": limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				last = stmt.ColumnText(0)
				ns := stmt.ColumnText(1)
				name := stmt.ColumnText(2)
				lastUpdated = stmt.ColumnInt64(3)
				names = append(names, keysToPath(prefix, root, kind, ns, name))
				return nil
			},
		})
	if err != nil {
		return nil, "", 0, fmt.Errorf("list names: %w", err)
	}
	return names, last, lastUpdated, nil
}

func listMetadata(conn *sqlite.Conn, path, cont string, since, limit int64) ([]string, string, int64, error) {
	var lastUpdated int64
	_, _, kind, namespace, _ := pathToKeys(path)
	if cont == "" {
		cont = "0"
	}
	var last string
	var metadataJSONs []string
	err := sqlitex.Execute(conn,
		`SELECT rowid, metadata, last_updated FROM metadata
                WHERE kind = :kind
                    AND (:namespace = '' OR namespace = :namespace)
                	AND rowid > :cont
                	AND (:since = 0 OR last_updated > :since)
				ORDER BY rowid
				LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":cont": cont, ":since": since, ":limit": limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				last = stmt.ColumnText(0)
				metadataJSON := stmt.ColumnText(1)
				lastUpdated = stmt.ColumnInt64(2)
				metadataJSONs = append(metadataJSONs, metadataJSON)
				return nil
			},
		})
	if err != nil {
		return nil, "", 0, fmt.Errorf("list metadata: %w", err)
	}
	return metadataJSONs, last, lastUpdated, nil
}

func listNamespaces(conn *sqlite.Conn) ([]string, error) {
	var namespaces []string
	err := sqlitex.Execute(conn,
		`SELECT DISTINCT namespace FROM metadata
				WHERE namespace != ''`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				namespace := stmt.ColumnText(0)
				namespaces = append(namespaces, namespace)
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	return namespaces, nil
}

// CleanOlderTimeSeries cleans up time series containers which are older than d.
func CleanOlderTimeSeries(conn *sqlite.Conn, d time.Duration) error {
	threshold := time.Now().Add(-d).String()
	err := sqlitex.Execute(conn,
		`DELETE FROM time_series WHERE reportTimestamp < ?`,
		&sqlitex.ExecOptions{
			Args: []any{threshold},
		})
	if err != nil {
		return fmt.Errorf("failed to cleanup time series: %w", err)
	}
	return nil
}

// DeleteTimeSeriesContainerEntries deletes all time series entries for a completed container.
func DeleteTimeSeriesContainerEntries(conn *sqlite.Conn, path string) error {
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.Execute(conn,
		`DELETE FROM time_series
					WHERE kind = ?
						AND namespace = ?
						AND name = ?`,
		&sqlitex.ExecOptions{
			Args: []any{kind, namespace, name},
		})
	if err != nil {
		return fmt.Errorf("delete all time series entries: %w", err)
	}
	return nil
}

// ListTimeSeriesContainers retrieves time series containers for a given path.
func ListTimeSeriesContainers(conn *sqlite.Conn, path string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	containers := make(map[string][]softwarecomposition.TimeSeriesContainers)
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.Execute(conn,
		`SELECT seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData
				FROM time_series
				WHERE kind = :kind
					AND namespace = :namespace
					AND name = :name
				ORDER BY reportTimestamp DESC`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":name": name},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				seriesID := stmt.ColumnText(0)
				tsSuffix := stmt.ColumnText(1)
				reportTimestamp := stmt.ColumnText(2)
				status := stmt.ColumnText(3)
				completion := stmt.ColumnText(4)
				previousReportTimestamp := stmt.ColumnText(5)
				hasData := stmt.ColumnBool(6)
				if _, ok := containers[seriesID]; !ok {
					containers[seriesID] = make([]softwarecomposition.TimeSeriesContainers, 0)
				}

				// Create a new TimeSeriesContainers instance and append it to the list
				containers[seriesID] = append(containers[seriesID], softwarecomposition.TimeSeriesContainers{
					Completion:              completion,
					HasData:                 hasData,
					PreviousReportTimestamp: previousReportTimestamp,
					ReportTimestamp:         reportTimestamp,
					Status:                  status,
					TsSuffix:                tsSuffix,
				})
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("list time series containers: %w", err)
	}
	return containers, nil
}

// ListTimeSeriesKeys retrieves all time series keys that have data.
func ListTimeSeriesKeys(conn *sqlite.Conn) ([]string, error) {
	var keys []string
	err := sqlitex.Execute(conn,
		`SELECT kind, namespace, name
				FROM time_series
				WHERE hasData == 1;`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				kind := stmt.ColumnText(0)
				ns := stmt.ColumnText(1)
				name := stmt.ColumnText(2)
				keys = append(keys, keysToPath("", "spdx.softwarecomposition.kubescape.io", kind, ns, name))
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("list ts keys: %w", err)
	}
	return keys, nil
}

// ReadMetadata reads metadata for the given path and returns it as a byte slice.
func ReadMetadata(conn *sqlite.Conn, path string) ([]byte, error) {
	_, _, kind, namespace, name := pathToKeys(path)
	var metadataJSON string
	err := sqlitex.Execute(conn,
		`SELECT metadata FROM metadata
				WHERE kind = :kind
				  AND namespace = :namespace
				  AND name = :name`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":name": name},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				metadataJSON = stmt.ColumnText(0)
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	if len(metadataJSON) == 0 {
		return nil, ErrMetadataNotFound
	}
	return []byte(metadataJSON), nil
}

func writeMetadata(conn *sqlite.Conn, path string, metadata runtime.Object) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return WriteJSON(conn, path, metadataJSON)
}

// WriteJSON writes the given JSON metadata to the database for the specified path.
func WriteJSON(conn *sqlite.Conn, path string, metadataJSON []byte) error {
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.Execute(conn,
		`INSERT OR REPLACE INTO metadata
				(kind, namespace, name, metadata) VALUES (?, ?, ?, ?)`,
		&sqlitex.ExecOptions{
			Args: []any{kind, namespace, name, metadataJSON},
		})
	if err != nil {
		return fmt.Errorf("insert metadata: %w", err)
	}
	return nil
}

// WriteTimeSeriesEntry writes a time series entry to the database.
func WriteTimeSeriesEntry(conn *sqlite.Conn, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	err := sqlitex.Execute(conn,
		`INSERT OR REPLACE INTO time_series
    			(kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData) 
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{
			Args: []any{kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData},
		})
	if err != nil {
		return fmt.Errorf("insert time series entry: %w", err)
	}
	return nil
}

// ReplaceTimeSeriesContainerEntries replaces time series entries for a given path and seriesID.
func ReplaceTimeSeriesContainerEntries(conn *sqlite.Conn, path, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	_, _, kind, namespace, name := pathToKeys(path)
	// FIXME we can probably optimize this, rather than deleting everything to add it back
	// delete old profiles
	tsSuffixes, err := json.Marshal(deleteTimeSeries)
	if err != nil {
		return fmt.Errorf("failed to marshal tsSuffixes: %w", err)
	}
	err = sqlitex.Execute(conn,
		`DELETE FROM time_series
				WHERE kind = ?
					AND namespace = ?
					AND name = ?
					AND seriesID = ?
					AND tsSuffix IN (SELECT value FROM json_each(?))`,
		&sqlitex.ExecOptions{
			Args: []any{kind, namespace, name, seriesID, string(tsSuffixes)},
		})
	if err != nil {
		return fmt.Errorf("delete time series entries: %w", err)
	}
	// insert new profiles
	for _, profile := range newTimeSeries {
		err := WriteTimeSeriesEntry(conn, kind, namespace, name, seriesID, profile.TsSuffix, profile.ReportTimestamp, profile.Status, profile.Completion, profile.PreviousReportTimestamp, profile.HasData)
		if err != nil {
			return fmt.Errorf("insert profile: %w", err)
		}
	}
	return nil
}
