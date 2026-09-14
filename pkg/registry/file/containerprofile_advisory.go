package file

// Advisory startup check (D) (.omc/plans/rollback-safety-guard.md): a
// purely informational census, run once at startup when
// cfg.ContainerProfileSqliteBackend is false, of how many ContainerProfile
// keys currently satisfy US-002's rollback read fallback predicate --
// i.e. how many keys are currently being served via the Part 2 fallback
// (serveFromPayloadsFallback, storage.go) rather than from a legacy .g
// file. It never fails startup: a query error is logged distinctly from a
// genuine zero count, and either way the caller proceeds.

import (
	"context"
	"fmt"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// advisoryFallbackExampleCap bounds how many example keys the census logs
// alongside the count, so a large count does not spam the log.
const advisoryFallbackExampleCap = 10

// FallbackCensusReport is the result of counting, at startup, how many
// ContainerProfile keys currently satisfy the rollback read fallback's
// 4-condition predicate.
type FallbackCensusReport struct {
	// Count is the total number of fallback-eligible keys found.
	Count int
	// ExampleKeys holds up to advisoryFallbackExampleCap of those keys'
	// full storage paths.
	ExampleKeys []string
}

// CensusFallbackEligibleContainerProfiles counts ContainerProfile keys
// currently satisfying the same 4-condition predicate as the rollback read
// fallback (serveFromPayloadsFallback, storage.go / readFallbackCandidate,
// sqlite.go): a metadata row exists, rv IS NOT NULL, is_time_series = 0,
// and a payloads row exists (the two row-existence conditions are enforced
// here by the JOIN itself).
//
// Bounded with its own timeout derived from ctx, rather than inheriting
// ctx's own deadline (or lack of one) directly -- the caller may pass an
// untimed signal context, and this census must never block startup
// indefinitely.
func CensusFallbackEligibleContainerProfiles(ctx context.Context, pool *sqlitemigration.Pool, timeout time.Duration) (FallbackCensusReport, error) {
	censusCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := pool.Take(censusCtx)
	if err != nil {
		return FallbackCensusReport{}, fmt.Errorf("fallback census: take connection: %w", err)
	}
	defer pool.Put(conn)

	var report FallbackCensusReport
	err = sqlitex.Execute(conn,
		`SELECT m.namespace, m.name
			FROM metadata m JOIN payloads p USING (kind, namespace, name)
			WHERE m.kind = :kind
			  AND m.rv IS NOT NULL
			  AND m.is_time_series = 0`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": ContainerProfileKind},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				if cerr := censusCtx.Err(); cerr != nil {
					return cerr
				}
				report.Count++
				if len(report.ExampleKeys) < advisoryFallbackExampleCap {
					key := K8sKeysToPath("", softwarecomposition.GroupName, ContainerProfileKind, "", stmt.ColumnText(0), stmt.ColumnText(1))
					report.ExampleKeys = append(report.ExampleKeys, key)
				}
				return nil
			},
		})
	if err != nil {
		return FallbackCensusReport{}, fmt.Errorf("fallback census: query: %w", err)
	}
	return report, nil
}

// LogFallbackEligibleContainerProfilesCensus runs the startup advisory
// census and logs its result -- intended to be called from main.go only
// when cfg.ContainerProfileSqliteBackend is false. It never calls Fatal and
// never returns an error to the caller: this check is purely informational
// and must never affect whether the server starts. A query failure is
// logged distinctly (Warning, carrying the error) from a genuine
// zero-count result (Info) so an operator never mistakes "the census
// itself failed" for "nothing to report".
func LogFallbackEligibleContainerProfilesCensus(ctx context.Context, pool *sqlitemigration.Pool, timeout time.Duration) {
	report, err := CensusFallbackEligibleContainerProfiles(ctx, pool, timeout)
	if err != nil {
		logger.L().Ctx(ctx).Warning("containerprofile rollback advisory census failed, skipping",
			helpers.Error(err))
		return
	}
	if report.Count == 0 {
		logger.L().Ctx(ctx).Info("containerprofile rollback advisory census: no fallback-eligible keys found")
		return
	}
	logger.L().Ctx(ctx).Warning("containerprofile rollback advisory census: fallback-eligible keys found",
		helpers.Int("count", report.Count), helpers.Interface("exampleKeys", report.ExampleKeys))
}
