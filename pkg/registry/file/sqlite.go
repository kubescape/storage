package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
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
			},
		},
		sqlitemigration.Options{
			PoolSize: size,
		})
}

// NewTestPool creates a new temporary SQLite connection (for testing only).
func NewTestPool(dir string) *sqlitemigration.Pool {
	return NewPool(filepath.Join(dir, "test.sq3"), 0)
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

func DeleteMetadata(conn *sqlite.Conn, path string, metadata runtime.Object) error {
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.ExecuteTransient(conn,
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

func listKeys(conn *sqlite.Conn, path, cont string, limit int64) ([]string, string, error) {
	prefix, root, kind, namespace, _ := pathToKeys(path)
	if cont == "" {
		cont = "0"
	}
	var last string
	var names []string
	err := sqlitex.Execute(conn,
		`SELECT rowid, namespace, name FROM metadata
                WHERE kind = :kind
                    AND (:namespace = '' OR namespace = :namespace)
                	AND rowid > :cont
				ORDER BY rowid
				LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":cont": cont, ":limit": limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				last = stmt.ColumnText(0)
				ns := stmt.ColumnText(1)
				name := stmt.ColumnText(2)
				names = append(names, fmt.Sprintf("%s/%s/%s/%s/%s", prefix, root, kind, ns, name))
				return nil
			},
		})
	if err != nil {
		return nil, "", fmt.Errorf("list names: %w", err)
	}
	return names, last, nil
}

func listMetadata(conn *sqlite.Conn, path, cont string, limit int64) ([]string, string, error) {
	_, _, kind, namespace, _ := pathToKeys(path)
	if cont == "" {
		cont = "0"
	}
	var last string
	var metadataJSONs []string
	err := sqlitex.Execute(conn,
		`SELECT rowid, metadata FROM metadata
                WHERE kind = :kind
                    AND (:namespace = '' OR namespace = :namespace)
                	AND rowid > :cont
				ORDER BY rowid
				LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": kind, ":namespace": namespace, ":cont": cont, ":limit": limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				last = stmt.ColumnText(0)
				metadataJSON := stmt.ColumnText(1)
				metadataJSONs = append(metadataJSONs, metadataJSON)
				return nil
			},
		})
	if err != nil {
		return nil, "", fmt.Errorf("list metadata: %w", err)
	}
	return metadataJSONs, last, nil
}

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
		return nil, errors.New("metadata not found")
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

func WriteJSON(conn *sqlite.Conn, path string, metadataJSON []byte) error {
	_, _, kind, namespace, name := pathToKeys(path)
	err := sqlitex.ExecuteTransient(conn,
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
