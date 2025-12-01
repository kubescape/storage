package file

import (
	"context"
	"errors"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrInvalidTransactionType is returned when a transaction type doesn't match the expected implementation
var ErrInvalidTransactionType = errors.New("invalid transaction type for this storage implementation")

// Transaction represents an abstract database transaction.
// Different storage backends (SQLite, PostgreSQL, etc.) should implement their own
// concrete transaction types that satisfy this interface.
// Implementations should use type assertions internally to access backend-specific functionality.
//
// To implement a custom transaction type for a new backend:
//
//	type PostgresTransaction struct {
//	    tx *sql.Tx
//	}
//
//	func (t *PostgresTransaction) transaction() {} // marker method
//
//	func (t *PostgresTransaction) Tx() *sql.Tx {
//	    return t.tx
//	}
type Transaction interface {
	// transaction is a marker method to prevent arbitrary types from satisfying this interface
	transaction()
}

// GetAggregatedDataFunc is a callback function for computing aggregated profile data.
// It takes a context, transaction, key, and parts map, returning status, completion, and checksum.
type GetAggregatedDataFunc func(ctx context.Context, tx Transaction, key string, parts map[string]string) (status string, completion string, checksum string)

// ContainerProfileStorage defines the storage operations for container profiles.
// This interface abstracts the underlying database implementation, allowing different
// backends (SQLite, PostgreSQL, etc.) to be used interchangeably.
//
// Transaction Management:
// - Implementations must provide transaction support via the TransactionManager methods
// - All data operations accept a Transaction parameter to enable atomic operations
// - Callers are responsible for proper transaction lifecycle management (begin/commit/rollback)
//
// Example usage with SQLite:
//
//	storage := NewContainerProfileStorageImpl(storageImpl, pool)
//	tx, err := storage.BeginTransaction(ctx)
//	if err != nil { ... }
//
//	// Use defer to ensure cleanup - it's a no-op if already committed
//	defer storage.CloseTransaction(tx)
//
//	err = storage.SaveContainerProfile(ctx, tx, key, profile)
//	if err != nil { ... }
//
//	err = storage.CommitTransaction(tx)
//	if err != nil { ... }
//
// Example usage with PostgreSQL (hypothetical):
//
//	storage := NewPostgresContainerProfileStorage(db)
//	tx, err := storage.BeginTransaction(ctx)
//	// ... same pattern as above
type ContainerProfileStorage interface {
	TransactionManager
	TimeSeriesOperations

	// DeleteContainerProfile deletes a container profile by key.
	DeleteContainerProfile(ctx context.Context, tx Transaction, key string) error

	// GetContainerProfile retrieves a complete container profile by key.
	GetContainerProfile(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error)

	// GetContainerProfileMetadata retrieves only the metadata of a container profile.
	// This is more efficient when only metadata is needed.
	GetContainerProfileMetadata(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error)

	// GetSbom retrieves an SBOM by key.
	// Returns storage.ErrCodeKeyNotFound if not found or not implemented.
	GetSbom(ctx context.Context, tx Transaction, key string) (softwarecomposition.SBOMSyft, error)

	// GetTsContainerProfile retrieves a time-series container profile.
	// This bypasses locking mechanisms used by GetContainerProfile.
	GetTsContainerProfile(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error)

	// SaveContainerProfile creates or updates a container profile.
	SaveContainerProfile(ctx context.Context, tx Transaction, key string, profile *softwarecomposition.ContainerProfile) error

	// UpdateApplicationProfile updates the application profile associated with a container profile.
	UpdateApplicationProfile(
		ctx context.Context,
		tx Transaction,
		key, prefix, root, namespace, slug, wlid string,
		instanceID interface{ GetStringNoContainer() string },
		profile *softwarecomposition.ContainerProfile,
		creationTimestamp metav1.Time,
		getAggregatedData GetAggregatedDataFunc,
	) error

	// UpdateNetworkNeighborhood updates the network neighborhood associated with a container profile.
	UpdateNetworkNeighborhood(
		ctx context.Context,
		tx Transaction,
		key, prefix, root, namespace, slug, wlid string,
		instanceID interface{ GetStringNoContainer() string },
		profile *softwarecomposition.ContainerProfile,
		creationTimestamp metav1.Time,
		getAggregatedData GetAggregatedDataFunc,
	) error
}

// TransactionManager handles database connection and transaction lifecycle.
// Implementations should manage connection pooling and transaction semantics
// appropriate for their backend.
type TransactionManager interface {
	// BeginTransaction starts a new transaction and returns it.
	// The caller must call either CommitTransaction or RollbackTransaction when done.
	BeginTransaction(ctx context.Context) (Transaction, error)

	// CommitTransaction commits the transaction.
	// After commit, the transaction should not be used again.
	CommitTransaction(tx Transaction) error

	// CloseTransaction closes the transaction and releases associated resources.
	// If the transaction was not committed, changes may be discarded (implementation-dependent).
	// This is safe to call multiple times or after a commit (it will be a no-op).
	// After close, the transaction should not be used again.
	CloseTransaction(tx Transaction)

	// BeginNestedTransaction starts a nested transaction (savepoint) within an existing transaction.
	// Returns a function that must be called with the error pointer to commit or rollback the savepoint.
	// Usage:
	//   endFn, err := storage.BeginNestedTransaction(tx)
	//   if err != nil { return err }
	//   err = doSomeWork(tx)
	//   endFn(&err) // commits savepoint if err is nil, rolls back otherwise
	BeginNestedTransaction(tx Transaction) (endFunc func(*error), err error)
}

// TimeSeriesOperations defines operations for managing time series data.
// These operations are used for tracking container profile versions over time.
type TimeSeriesOperations interface {
	// ListTimeSeriesExpired returns keys for time series entries older than the given duration.
	// These represent profiles that have exceeded their tracking threshold.
	ListTimeSeriesExpired(ctx context.Context, tx Transaction, threshold time.Duration) ([]string, error)

	// ListTimeSeriesWithData returns keys for all time series entries that have pending data.
	ListTimeSeriesWithData(ctx context.Context, tx Transaction) ([]string, error)

	// ListTimeSeriesContainers retrieves time series container information for a given key.
	// Returns a map of seriesID to slice of TimeSeriesContainers.
	ListTimeSeriesContainers(ctx context.Context, tx Transaction, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error)

	// DeleteTimeSeriesContainerEntries removes all time series entries for a given key.
	DeleteTimeSeriesContainerEntries(ctx context.Context, tx Transaction, key string) error

	// ReplaceTimeSeriesContainerEntries replaces time series entries for a given key and seriesID.
	// It deletes entries in deleteTimeSeries and inserts newTimeSeries.
	ReplaceTimeSeriesContainerEntries(ctx context.Context, tx Transaction, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error
}
