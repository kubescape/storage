package config

import (
	"fmt"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/spf13/viper"
)

type KindQueueConfig struct {
	QueueLength   int `mapstructure:"queueLength"`
	WorkerCount   int `mapstructure:"workerCount"`
	MaxObjectSize int `mapstructure:"maxObjectSize"`
}

type Config struct {
	CleanupInterval               time.Duration      `mapstructure:"cleanupInterval"`
	DefaultNamespace              string             `mapstructure:"defaultNamespace"`
	HostType                      armotypes.HostType `mapstructure:"hostType"`
	DisableVirtualCRDs            bool               `mapstructure:"disableVirtualCRDs"`
	DisableSeccompProfileEndpoint bool               `mapstructure:"disableSeccompProfileEndpoint"`
	ExcludeJsonPaths              []string           `mapstructure:"excludeJsonPaths"`
	MaxContainerProfileSize       int                `mapstructure:"maxContainerProfileSize"`
	MaxSniffingTime               time.Duration      `mapstructure:"maxSniffingTimePerContainer"`
	RateLimitPerClient            float64            `mapstructure:"rateLimitPerClient"`
	RateLimitTotal                int                `mapstructure:"rateLimitTotal"`
	ServerBindAddress             string             `mapstructure:"serverBindAddress"`
	ServerBindPort                int                `mapstructure:"serverBindPort"`
	TlsClientCaFile               string             `mapstructure:"tlsClientCaFile"`
	TlsServerCertFile             string             `mapstructure:"tlsServerCertFile"`
	TlsServerKeyFile              string             `mapstructure:"tlsServerKeyFile"`

	// SqlitePoolSize is the capacity of the SQLite connection pool. Defaults
	// to file.DefaultPoolSize (10) when unset. This is a cheap, reversible
	// tuning knob for Phase 0 of the storage-locking investigation (see
	// docs/features/storage-lock-pool-metrics.md) — it does not change any
	// lock/connection acquisition ordering.
	SqlitePoolSize int `mapstructure:"sqlitePoolSize"`
	// SqliteBusyTimeout is the busy-timeout applied to every pooled SQLite
	// connection. Defaults to file.DefaultBusyTimeout (60s) when unset.
	SqliteBusyTimeout time.Duration `mapstructure:"sqliteBusyTimeout"`
	// PoolTimeout bounds how long a caller blocks in pool.Take() waiting for a free
	// connection from the SQLite connection pool, before failing fast with a
	// ServerTimeout+Retry-After signal. Defaults to file.DefaultPoolTimeout (5s) when
	// unset. This is distinct from SqliteBusyTimeout, which governs SQLite's own
	// internal busy-handler on a single already-acquired connection. See
	// docs/features/storage-lock-pool-metrics.md.
	PoolTimeout time.Duration `mapstructure:"poolTimeout"`

	// SingleWriterEnabled gates the single-dedicated-writer + priority-queue
	// write path prototyped on spike/single-writer-priority-queue (see
	// pkg/registry/file/singlewriter.go). Defaults to false: when unset,
	// Create/GuaranteedUpdate/SaveContainerProfile behave exactly as they did
	// before that path existed. This is a spike/prototype flag, not yet
	// validated for production use.
	SingleWriterEnabled bool `mapstructure:"singleWriterEnabled"`

	// CustomKnownServersRestEnabled gates the hand-written rest.Storage
	// implementation for the knownservers resource (see
	// pkg/registry/softwarecomposition/knownservers/custom_rest.go), built as
	// Phase 4's first per-resource migration off genericregistry.Store (see
	// docs/features/generic-rest-storage-phase4.md). Defaults to true as of
	// the live validation on armo-dev-stage (2026-08-31, zero regressions
	// found across all 11 Phase 4 resources): when unset, pkg/apiserver/apiserver.go
	// registers knownservers via the NEW genericrest.Store-based custom_rest.go
	// implementation. Set to false to fall back to the OLD
	// genericregistry.Store-based knownservers.NewREST, which is kept alive
	// as the reference implementation for differential testing regardless of
	// this flag's value.
	CustomKnownServersRestEnabled bool `mapstructure:"customKnownServersRestEnabled"`

	// CustomOpenVulnerabilityExchangeRestEnabled gates the hand-written
	// rest.Storage implementation for the openvulnerabilityexchangecontainers
	// resource (see
	// pkg/registry/softwarecomposition/openvulnerabilityexchange/custom_rest.go),
	// built as Phase 4's second per-resource migration off
	// genericregistry.Store (see docs/features/generic-rest-storage-phase4.md).
	// Defaults to true as of the live validation on armo-dev-stage
	// (2026-08-31, zero regressions found across all 11 Phase 4 resources):
	// when unset, pkg/apiserver/apiserver.go registers
	// openvulnerabilityexchangecontainers via the NEW genericrest.Store-based
	// custom_rest.go implementation. Set to false to fall back to the OLD
	// genericregistry.Store-based openvulnerabilityexchange.NewREST, which is
	// kept alive as the reference implementation for differential testing
	// regardless of this flag's value.
	CustomOpenVulnerabilityExchangeRestEnabled bool `mapstructure:"customOpenVulnerabilityExchangeRestEnabled"`

	// CustomContainerProfileRestEnabled gates the hand-written rest.Storage
	// implementation for the containerprofiles resource (see
	// pkg/registry/softwarecomposition/containerprofile/custom_rest.go),
	// built as Phase 4's third per-resource migration off
	// genericregistry.Store (see docs/features/generic-rest-storage-phase4.md).
	// Defaults to true as of the live validation on armo-dev-stage
	// (2026-08-31, zero regressions found across all 11 Phase 4 resources):
	// when unset, pkg/apiserver/apiserver.go registers containerprofiles via
	// the NEW genericrest.Store-based custom_rest.go implementation. Set to
	// false to fall back to the OLD genericregistry.Store-based
	// containerprofile.NewREST, which is kept alive as the reference
	// implementation for differential testing regardless of this flag's
	// value.
	CustomContainerProfileRestEnabled bool `mapstructure:"customContainerProfileRestEnabled"`

	// The following gate the remaining Phase 4 per-resource rest.Storage
	// migrations off genericregistry.Store (see
	// docs/features/generic-rest-storage-phase4.md), following the same pattern as
	// CustomContainerProfileRestEnabled above: each defaults to true as of
	// the live validation on armo-dev-stage (2026-08-31, zero regressions
	// found across all 11 Phase 4 resources), and the OLD
	// genericregistry.Store-based NewREST for that resource remains
	// available as a fallback (set the flag to false) and as the
	// differential-testing reference regardless of the flag's value.
	CustomCollapseConfigurationRestEnabled            bool `mapstructure:"customCollapseConfigurationRestEnabled"`
	CustomSBOMSyftFilteredRestEnabled                 bool `mapstructure:"customSBOMSyftFilteredRestEnabled"`
	CustomSBOMSyftRestEnabled                         bool `mapstructure:"customSBOMSyftRestEnabled"`
	CustomSeccompProfileRestEnabled                   bool `mapstructure:"customSeccompProfileRestEnabled"`
	CustomVulnerabilityManifestRestEnabled            bool `mapstructure:"customVulnerabilityManifestRestEnabled"`
	CustomVulnerabilityManifestSummaryRestEnabled     bool `mapstructure:"customVulnerabilityManifestSummaryRestEnabled"`
	CustomWorkloadConfigurationScanRestEnabled        bool `mapstructure:"customWorkloadConfigurationScanRestEnabled"`
	CustomWorkloadConfigurationScanSummaryRestEnabled bool `mapstructure:"customWorkloadConfigurationScanSummaryRestEnabled"`

	// New fields for per-kind queue/worker/object size config
	KindQueues           map[string]KindQueueConfig `mapstructure:"kindQueues"`
	DefaultQueueLength   int                        `mapstructure:"defaultQueueLength"`
	DefaultWorkerCount   int                        `mapstructure:"defaultWorkerCount"`
	DefaultMaxObjectSize int                        `mapstructure:"defaultMaxObjectSize"`

	// Debugging
	QueueManagerEnabled       bool `mapstructure:"queueManagerEnabled"`
	QueueTimeoutPrint         bool `mapstructure:"queueTimeoutPrint"`
	QueueTimeout              int  `mapstructure:"queueTimeout"`
	QueueProcessingStatsPrint bool `mapstructure:"queueProcessingStatsPrint"`
}

// LoadConfig reads configuration from file or environment variables.
func LoadConfig(path string) (Config, error) {
	v := viper.New()
	v.AddConfigPath(path)
	v.SetConfigName("config")
	v.SetConfigType("json")

	v.SetDefault("cleanupInterval", 24*time.Hour)
	v.SetDefault("defaultNamespace", "kubescape")
	v.SetDefault("maxContainerProfileSize", 40000)
	v.SetDefault("rateLimitTotal", 10)
	v.SetDefault("serverBindAddress", "::")
	v.SetDefault("serverBindPort", 8443)
	// The custom rest.Storage implementations were Phase 4 spikes/prototypes;
	// each has since been live-validated against armo-dev-stage (2026-08-31,
	// zero regressions across all 11 resources) and now defaults to true. Set
	// any of these to false to fall back to that resource's OLD
	// genericregistry.Store-based implementation.
	v.SetDefault("customKnownServersRestEnabled", true)
	v.SetDefault("customOpenVulnerabilityExchangeRestEnabled", true)
	v.SetDefault("customContainerProfileRestEnabled", true)
	v.SetDefault("customCollapseConfigurationRestEnabled", true)
	v.SetDefault("customSBOMSyftFilteredRestEnabled", true)
	v.SetDefault("customSBOMSyftRestEnabled", true)
	v.SetDefault("customSeccompProfileRestEnabled", true)
	v.SetDefault("customVulnerabilityManifestRestEnabled", true)
	v.SetDefault("customVulnerabilityManifestSummaryRestEnabled", true)
	v.SetDefault("customWorkloadConfigurationScanRestEnabled", true)
	v.SetDefault("customWorkloadConfigurationScanSummaryRestEnabled", true)
	v.SetDefault("defaultQueueLength", 100)
	v.SetDefault("defaultWorkerCount", 2)
	v.SetDefault("defaultMaxObjectSize", 400000)
	v.SetDefault("queueManagerEnabled", false)
	v.SetDefault("queueTimeoutPrint", false)
	v.SetDefault("queueTimeout", 60)
	v.SetDefault("queueProcessingStatsPrint", false)
	// Keep in sync with file.DefaultPoolSize / file.DefaultBusyTimeout
	// (pkg/registry/file/sqlite.go) — these are the values already hardcoded
	// there today; making them config-driven must be a no-op by default.
	v.SetDefault("sqlitePoolSize", 10)
	v.SetDefault("sqliteBusyTimeout", 60*time.Second)
	// Keep in sync with file.DefaultPoolTimeout (pkg/registry/file/storage.go) — this is
	// the value already hardcoded there today; making it config-driven must be a no-op
	// by default.
	v.SetDefault("poolTimeout", 5*time.Second)
	// Keep in sync with file.SetSingleWriterEnabled's default (false) — the
	// single-writer/priority-queue write path is a spike/prototype, not yet
	// validated for production use.
	v.SetDefault("singleWriterEnabled", false)
	v.SetDefault("kindQueues", map[string]KindQueueConfig{
		"containerprofiles": {
			QueueLength:   50,
			WorkerCount:   1,
			MaxObjectSize: 2500000,
		},
		"openvulnerabilityexchangecontainers": {
			QueueLength:   50,
			WorkerCount:   1,
			MaxObjectSize: 500000,
		},
		"sbomsyftfiltereds": {
			QueueLength:   50,
			WorkerCount:   1,
			MaxObjectSize: 20000000,
		},
		"sbomsyfts": {
			QueueLength:   50,
			WorkerCount:   1,
			MaxObjectSize: 100000000,
		},
		"vulnerabilitymanifests": {
			QueueLength:   50,
			WorkerCount:   1,
			MaxObjectSize: 10000000,
		},
	})

	err := v.ReadInConfig()
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = v.Unmarshal(&config)
	if err != nil {
		return Config{}, err
	}

	// Read hostType explicitly to handle cases where it's not set in the struct correctly after unmarshal
	if ht := v.GetString("hostType"); ht != "" {
		config.HostType = armotypes.HostType(ht)
	}

	// Validate and normalize HostType
	if config.HostType == "" {
		config.HostType = armotypes.HostTypeKubernetes
	}

	switch config.HostType {
	case armotypes.HostTypeKubernetes,
		armotypes.HostTypeEcsEc2,
		armotypes.HostTypeEcsFargate,
		armotypes.HostTypeAks,
		armotypes.HostTypeAci,
		armotypes.HostTypeAzureVm,
		armotypes.HostTypeCloudRun,
		armotypes.HostTypeAutopilot,
		armotypes.HostTypeDoks,
		armotypes.HostTypeDroplet,
		armotypes.HostTypeEc2,
		armotypes.HostTypeEksEc2,
		armotypes.HostTypeEksFargate,
		armotypes.HostTypeGce,
		armotypes.HostTypeGke,
		armotypes.HostTypeOther:
		// valid
	default:
		return Config{}, fmt.Errorf("unsupported hostType: %s", config.HostType)
	}

	return config, nil
}
