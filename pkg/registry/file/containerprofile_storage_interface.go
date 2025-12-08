package file

import (
	"context"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetAggregatedDataFunc is a callback function for computing aggregated profile data.
// It takes a context, key, and parts map, returning status, completion, and checksum.
// The context should contain the database connection (via context.WithValue with key connKey).
type GetAggregatedDataFunc func(ctx context.Context, key string, parts map[string]string) (status string, completion string, checksum string)

// ContainerProfileStorage defines the storage operations for container profiles.
// This interface abstracts the underlying database implementation, allowing different
// backends (SQLite, PostgreSQL, etc.) to be used interchangeably.
//
// Connection Management:
// - The database connection is stored in the context using context.WithValue with key connKey
// - Call BeginTransaction to acquire a connection and get a context with the connection embedded
// - All data operations extract the connection from the context
// - Use the cleanup function returned by BeginTransaction to return the connection to the pool
//
// Example usage:
//
//	storage := NewContainerProfileStorageImpl(storageImpl, pool)
//	ctx, cleanup, err := storage.WithConnection(ctx)
//	if err != nil { ... }
//	defer cleanup()
//
//	err = storage.SaveContainerProfile(ctx, key, profile)
//	if err != nil { ... }
//
//	// For transactions (savepoints):
//	endFn, err := storage.BeginTransaction(ctx)
//	if err != nil { ... }
//	err = doSomeWork(ctx)
//	endFn(&err) // commits if err is nil, rolls back otherwise
type ContainerProfileStorage interface {
	TransactionManager
	TimeSeriesOperations

	// DeleteContainerProfile deletes a container profile by key.
	DeleteContainerProfile(ctx context.Context, key string) error

	// GetContainerProfile retrieves a complete container profile by key.
	GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error)

	// GetContainerProfileMetadata retrieves only the metadata of a container profile.
	// This is more efficient when only metadata is needed.
	GetContainerProfileMetadata(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error)

	// GetSbom retrieves an SBOM by key.
	// Returns storage.ErrCodeKeyNotFound if not found or not implemented.
	GetSbom(ctx context.Context, key string) (softwarecomposition.SBOMSyft, error)

	// GetTsContainerProfile retrieves a time-series container profile.
	// This bypasses locking mechanisms used by GetContainerProfile.
	GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error)

	// SaveContainerProfile creates or updates a container profile.
	SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error

	// UpdateApplicationProfile updates the application profile associated with a container profile.
	UpdateApplicationProfile(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataFunc) error

	// UpdateNetworkNeighborhood updates the network neighborhood associated with a container profile.
	UpdateNetworkNeighborhood(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataFunc) error
}

// TransactionManager handles database connection and transaction lifecycle.
// Implementations should manage connection pooling and transaction semantics
// appropriate for their backend.
//
// The connection is stored in the context using context.WithValue with key connKey.
// All storage methods extract the connection from the context.
type TransactionManager interface {
	// WithConnection acquires a connection from the pool and returns a new context
	// with the connection embedded, plus a cleanup function to return the connection to the pool.
	// The cleanup function is safe to call multiple times.
	// Usage:
	//   ctx, cleanup, err := storage.WithConnection(ctx)
	//   if err != nil { return err }
	//   defer cleanup()
	//   // ... do work with ctx ...
	WithConnection(ctx context.Context) (context.Context, func(), error)

	// BeginTransaction starts a SQLite transaction (savepoint) and returns a function
	// to commit or rollback based on the error state.
	// The connection must already be in the context (from WithConnection).
	// Returns a function that must be called with the error pointer to commit or rollback the savepoint.
	// Usage:
	//   endFn, err := storage.BeginTransaction(ctx)
	//   if err != nil { return err }
	//   err = doSomeWork(ctx)
	//   endFn(&err) // commits savepoint if err is nil, rolls back otherwise
	BeginTransaction(ctx context.Context) (endFunc func(*error), err error)
}

// TimeSeriesOperations defines operations for managing time series data.
// These operations are used for tracking container profile versions over time.
type TimeSeriesOperations interface {
	// ListTimeSeriesExpired returns keys for time series entries older than the given duration.
	// These represent profiles that have exceeded their tracking threshold.
	ListTimeSeriesExpired(ctx context.Context, threshold time.Duration) ([]string, error)

	// ListTimeSeriesWithData returns keys for all time series entries that have pending data.
	ListTimeSeriesWithData(ctx context.Context) ([]string, error)

	// ListTimeSeriesContainers retrieves time series container information for a given key.
	// Returns a map of seriesID to slice of TimeSeriesContainers.
	ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error)

	// DeleteTimeSeriesContainerEntries removes all time series entries for a given key.
	DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error

	// ReplaceTimeSeriesContainerEntries replaces time series entries for a given key and seriesID.
	// It deletes entries in deleteTimeSeries and inserts newTimeSeries.
	ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error
}
