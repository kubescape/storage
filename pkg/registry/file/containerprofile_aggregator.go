package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
)

// ComputeAggregatedData computes aggregated profile data for an aggregated profile.
// It is a package-level helper so both the processor and storage implementation can reuse it
// without introducing a dependency between storage and processor types.
//
// Parameters:
// - storage: any implementation of ContainerProfileStorage (declared in this package).
// - ctx: parent context (used to create short-lived timeouts for individual profile lookups).
// - key: the aggregated profile key (used only for logging).
// - parts: map of child profile keys -> checksum (may be modified in-place; missing checksums will be filled).
//
// Returns: (status, completion, checksum)
func ComputeAggregatedData(storage ContainerProfileStorage, ctx context.Context, key string, parts map[string]string) (string, string, string) {
	// initialize local counters and defaults
	mainContainers := 0
	completed := 0
	full := 0
	var tooLarge bool
	status := helpers.Learning
	completion := helpers.Partial
	hasher := sha256.New()

	// handle nil parts defensively
	if parts == nil {
		parts = map[string]string{}
	}

	// deterministic ordering
	keys := make([]string, 0, len(parts))
	for k := range parts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		// use a short timeout per-profile to avoid long hangs
		cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)

		profile, err := storage.GetContainerProfileMetadata(cpCtx, k)

		// explicitly cancel the context right away to avoid accumulating defers in loops
		cpCancel()

		if err != nil {
			// preserve previous behavior: log debug and skip problematic entries
			logger.L().Debug("ComputeAggregatedData - failed to get profile", loggerhelpers.Error(err), loggerhelpers.String("key", k))
			continue
		}

		// only main containers are considered for aggregated status
		if profile.Annotations[helpers.ContainerTypeMetadataKey] == "containers" {
			mainContainers++
			if profile.Annotations[helpers.StatusMetadataKey] == helpers.Completed {
				completed++
			}
		}
		if profile.Annotations[helpers.CompletionMetadataKey] == helpers.Full {
			full++
		}
		if profile.Annotations[helpers.StatusMetadataKey] == helpers.TooLarge {
			tooLarge = true
		}

		checksum := profile.Annotations[helpers.SyncChecksumMetadataKey]
		parts[k] = checksum
		hasher.Write([]byte(checksum))
	}

	// derive aggregated status
	if completed == mainContainers && mainContainers > 0 {
		status = helpers.Completed
	} else if tooLarge {
		status = helpers.TooLarge
	}

	// derive aggregated completion
	if full == len(parts) {
		completion = helpers.Full
	}

	hash := hex.EncodeToString(hasher.Sum(nil))

	logger.L().Debug("ComputeAggregatedData - returning",
		loggerhelpers.String("key", key),
		loggerhelpers.Int("mainContainers", mainContainers),
		loggerhelpers.Int("completed", completed),
		loggerhelpers.Int("full", full),
		loggerhelpers.String("status", status),
		loggerhelpers.String("completion", completion),
		loggerhelpers.String("hash", hash),
	)

	return status, completion, hash
}
