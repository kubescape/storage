package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/spf13/viper"
)

// CustomRestEnabledEnvVar is the single env var that overrides every
// Custom<Resource>RestEnabled flag at once, for deployments that want one
// on/off switch for the Phase 4 rest.Storage migration instead of setting
// 11 individual config.json fields. See LoadConfig.
const CustomRestEnabledEnvVar = "CUSTOM_REST_ENABLED"

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
	// write path (see pkg/registry/file/singlewriter.go). Defaults to true.
	SingleWriterEnabled bool `mapstructure:"singleWriterEnabled"`

	// CustomKnownServersRestEnabled gates the hand-written rest.Storage
	// implementation for the knownservers resource (see
	// pkg/registry/softwarecomposition/knownservers/custom_rest.go), built as
	// Phase 4's first per-resource migration off genericregistry.Store (see
	// docs/features/generic-rest-storage-phase4.md). Defaults to false: when
	// unset, pkg/apiserver/apiserver.go registers knownservers via the OLD
	// genericregistry.Store-based knownservers.NewREST, exactly as before
	// this flag existed, even though live validation on armo-dev-stage
	// (2026-08-31) found zero regressions across all 11 Phase 4 resources --
	// that validation covers one cluster, not every deployment of this
	// binary, so the code-level default stays conservative. The old
	// implementation is kept alive as the reference implementation for
	// differential testing regardless of this flag's value. See
	// CustomRestEnabledEnvVar for a single env var that opts a deployment
	// into all 11 flags at once (e.g. for a design-partner test environment)
	// without changing this default for everyone else.
	CustomKnownServersRestEnabled bool `mapstructure:"customKnownServersRestEnabled"`

	// CustomOpenVulnerabilityExchangeRestEnabled gates the hand-written
	// rest.Storage implementation for the openvulnerabilityexchangecontainers
	// resource (see
	// pkg/registry/softwarecomposition/openvulnerabilityexchange/custom_rest.go),
	// built as Phase 4's second per-resource migration off
	// genericregistry.Store (see docs/features/generic-rest-storage-phase4.md).
	// Defaults to false, for the same reason as CustomKnownServersRestEnabled
	// above: when unset, pkg/apiserver/apiserver.go registers
	// openvulnerabilityexchangecontainers via the OLD genericregistry.Store-based
	// openvulnerabilityexchange.NewREST. The old implementation is kept alive
	// as the reference implementation for differential testing regardless of
	// this flag's value.
	CustomOpenVulnerabilityExchangeRestEnabled bool `mapstructure:"customOpenVulnerabilityExchangeRestEnabled"`

	// CustomContainerProfileRestEnabled gates the hand-written rest.Storage
	// implementation for the containerprofiles resource (see
	// pkg/registry/softwarecomposition/containerprofile/custom_rest.go),
	// built as Phase 4's third per-resource migration off
	// genericregistry.Store (see docs/features/generic-rest-storage-phase4.md).
	// Defaults to false, for the same reason as CustomKnownServersRestEnabled
	// above: when unset, pkg/apiserver/apiserver.go registers containerprofiles
	// via the OLD genericregistry.Store-based containerprofile.NewREST. The old
	// implementation is kept alive as the reference implementation for
	// differential testing regardless of this flag's value.
	CustomContainerProfileRestEnabled bool `mapstructure:"customContainerProfileRestEnabled"`

	// The following gate the remaining Phase 4 per-resource rest.Storage
	// migrations off genericregistry.Store (see
	// docs/features/generic-rest-storage-phase4.md), following the same pattern as
	// CustomContainerProfileRestEnabled above: each defaults to false, and
	// the OLD genericregistry.Store-based NewREST for that resource remains
	// the default and the differential-testing reference regardless of the
	// flag's value.
	//
	// All 11 of these flags can also be set at once via the CUSTOM_REST_ENABLED
	// env var (see CustomRestEnabledEnvVar/LoadConfig), which -- when set --
	// overrides every flag below regardless of what config.json says. This is
	// the intended way to opt a specific deployment (e.g. a design-partner
	// test environment) into the new REST path without changing the
	// conservative code-level default for every other deployment of this
	// binary.
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
	// The custom rest.Storage implementations are Phase 4 migrations off
	// genericregistry.Store, live-validated against armo-dev-stage (2026-08-31,
	// zero regressions across all 11 resources) but still default to false:
	// that validation covers one cluster, not every deployment of this binary,
	// so the OLD genericregistry.Store-based implementation stays the default
	// everywhere until a broader soak justifies flipping it. Set any of these
	// to true individually, or set CUSTOM_REST_ENABLED to opt a whole
	// deployment into all 11 at once (see CustomRestEnabledEnvVar).
	v.SetDefault("customKnownServersRestEnabled", false)
	v.SetDefault("customOpenVulnerabilityExchangeRestEnabled", false)
	v.SetDefault("customContainerProfileRestEnabled", false)
	v.SetDefault("customCollapseConfigurationRestEnabled", false)
	v.SetDefault("customSBOMSyftFilteredRestEnabled", false)
	v.SetDefault("customSBOMSyftRestEnabled", false)
	v.SetDefault("customSeccompProfileRestEnabled", false)
	v.SetDefault("customVulnerabilityManifestRestEnabled", false)
	v.SetDefault("customVulnerabilityManifestSummaryRestEnabled", false)
	v.SetDefault("customWorkloadConfigurationScanRestEnabled", false)
	v.SetDefault("customWorkloadConfigurationScanSummaryRestEnabled", false)
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
	v.SetDefault("poolTimeout", 15*time.Second)
	// Keep in sync with file.SetSingleWriterEnabled's default (true) — the
	// single-writer/priority-queue write path is enabled by default.
	v.SetDefault("singleWriterEnabled", true)
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

	if err := applyCustomRestEnabledEnvVar(&config); err != nil {
		return Config{}, err
	}

	return config, nil
}

// applyCustomRestEnabledEnvVar overrides every Custom<Resource>RestEnabled
// flag with the CUSTOM_REST_ENABLED env var's value, when set. Unset (empty)
// leaves every flag exactly as config.json/defaults computed it -- this is a
// pure override, never a default, so it never masks a config.json parse
// error and never changes behavior for a deployment that doesn't set it.
func applyCustomRestEnabledEnvVar(config *Config) error {
	raw, ok := os.LookupEnv(CustomRestEnabledEnvVar)
	if !ok || raw == "" {
		return nil
	}

	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("invalid %s value %q: %w", CustomRestEnabledEnvVar, raw, err)
	}

	config.CustomKnownServersRestEnabled = enabled
	config.CustomOpenVulnerabilityExchangeRestEnabled = enabled
	config.CustomContainerProfileRestEnabled = enabled
	config.CustomCollapseConfigurationRestEnabled = enabled
	config.CustomSBOMSyftFilteredRestEnabled = enabled
	config.CustomSBOMSyftRestEnabled = enabled
	config.CustomSeccompProfileRestEnabled = enabled
	config.CustomVulnerabilityManifestRestEnabled = enabled
	config.CustomVulnerabilityManifestSummaryRestEnabled = enabled
	config.CustomWorkloadConfigurationScanRestEnabled = enabled
	config.CustomWorkloadConfigurationScanSummaryRestEnabled = enabled
	return nil
}
