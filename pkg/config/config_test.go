package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    Config
		wantErr bool
	}{
		{
			name: "TestLoadConfig",
			path: "../../configuration",
			want: Config{
				CleanupInterval:         24 * time.Hour,
				DefaultNamespace:        "kubescape",
				HostType:                armotypes.HostTypeKubernetes,
				ExcludeJsonPaths:        []string{".containers[*].env[?(@.name==\"KUBECONFIG\")]"},
				MaxContainerProfileSize: 40000,
				RateLimitTotal:          10,
				ServerBindAddress:       "::",
				ServerBindPort:          8443,
				KindQueues: map[string]KindQueueConfig{
					"applicationprofiles": {
						QueueLength:   50,
						WorkerCount:   2,
						MaxObjectSize: 20000000,
					},
					"containerprofiles": {
						QueueLength:   50,
						WorkerCount:   2,
						MaxObjectSize: 2500000,
					},
					"networkneighborhoods": {
						QueueLength:   50,
						WorkerCount:   2,
						MaxObjectSize: 10000000,
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
				},
				DefaultQueueLength:   100,
				DefaultWorkerCount:   2,
				DefaultMaxObjectSize: 400000,
				QueueManagerEnabled:  true,
				QueueTimeout:         60,
				SqlitePoolSize:       10,
				SqliteBusyTimeout:    60 * time.Second,
				PoolTimeout:          15 * time.Second,
				SingleWriterEnabled:  true,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadConfig(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHostTypeValidation(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		configJSON   string
		wantHostType armotypes.HostType
		wantErr      bool
	}{
		{
			name:         "Kubernetes HostType",
			configJSON:   `{"hostType": "kubernetes"}`,
			wantHostType: armotypes.HostTypeKubernetes,
			wantErr:      false,
		},
		{
			name:         "ECS EC2 HostType",
			configJSON:   `{"hostType": "ecs-ec2"}`,
			wantHostType: armotypes.HostTypeEcsEc2,
			wantErr:      false,
		},
		{
			name:         "EC2 HostType",
			configJSON:   `{"hostType": "ec2"}`,
			wantHostType: armotypes.HostTypeEc2,
			wantErr:      false,
		},
		{
			name:         "Other HostType",
			configJSON:   `{"hostType": "other"}`,
			wantHostType: armotypes.HostTypeOther,
			wantErr:      false,
		},
		{
			name:         "Empty HostType defaults to Kubernetes",
			configJSON:   `{}`,
			wantHostType: armotypes.HostTypeKubernetes,
			wantErr:      false,
		},
		{
			name:       "Invalid HostType returns error",
			configJSON: `{"hostType": "invalid"}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(tempDir, tt.name)
			err := os.MkdirAll(dir, 0755)
			assert.NoError(t, err)

			err = os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.configJSON), 0644)
			assert.NoError(t, err)

			got, err := LoadConfig(dir)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantHostType, got.HostType)
			}
		})
	}
}

// TestCustomRestEnabledEnvVar guards the single-knob override: setting
// CUSTOM_REST_ENABLED must override every Custom<Resource>RestEnabled flag at
// once, regardless of what config.json says for the individual flags, while
// leaving it unset must leave config.json's per-resource values untouched.
func TestCustomRestEnabledEnvVar(t *testing.T) {
	allFlags := func(c Config) []bool {
		return []bool{
			c.CustomKnownServersRestEnabled,
			c.CustomOpenVulnerabilityExchangeRestEnabled,
			c.CustomContainerProfileRestEnabled,
			c.CustomCollapseConfigurationRestEnabled,
			c.CustomSBOMSyftFilteredRestEnabled,
			c.CustomSBOMSyftRestEnabled,
			c.CustomSeccompProfileRestEnabled,
			c.CustomVulnerabilityManifestRestEnabled,
			c.CustomVulnerabilityManifestSummaryRestEnabled,
			c.CustomWorkloadConfigurationScanRestEnabled,
			c.CustomWorkloadConfigurationScanSummaryRestEnabled,
		}
	}

	tempDir := t.TempDir()
	dir := filepath.Join(tempDir, "mixed-flags")
	require.NoError(t, os.MkdirAll(dir, 0755))
	// A deliberately mixed config: some resources opted out individually.
	// The env var, when set, must win over every one of these regardless.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{
		"customKnownServersRestEnabled": false,
		"customContainerProfileRestEnabled": true,
		"customSBOMSyftRestEnabled": false
	}`), 0644))

	t.Run("unset leaves config.json's per-resource values untouched", func(t *testing.T) {
		got, err := LoadConfig(dir)
		require.NoError(t, err)
		assert.False(t, got.CustomKnownServersRestEnabled)
		assert.True(t, got.CustomContainerProfileRestEnabled)
		assert.False(t, got.CustomSBOMSyftRestEnabled)
		// Untouched-in-config.json flags still fall back to the false default.
		assert.False(t, got.CustomCollapseConfigurationRestEnabled)
	})

	t.Run("true overrides every flag to true", func(t *testing.T) {
		t.Setenv(CustomRestEnabledEnvVar, "true")
		got, err := LoadConfig(dir)
		require.NoError(t, err)
		for _, v := range allFlags(got) {
			assert.True(t, v)
		}
	})

	t.Run("false overrides every flag to false", func(t *testing.T) {
		t.Setenv(CustomRestEnabledEnvVar, "false")
		got, err := LoadConfig(dir)
		require.NoError(t, err)
		for _, v := range allFlags(got) {
			assert.False(t, v)
		}
	})

	t.Run("invalid value is a loud error, not silently ignored", func(t *testing.T) {
		t.Setenv(CustomRestEnabledEnvVar, "not-a-bool")
		_, err := LoadConfig(dir)
		assert.Error(t, err)
	})
}

// TestSqlitePoolConfig verifies the new SqlitePoolSize/SqliteBusyTimeout knobs
// default to today's previously-hardcoded values (10 / 60s) when unset, and
// are read correctly from config.json when set. This is a Phase 0
// (docs/features/storage-lock-pool-metrics.md) config-only change: the
// defaults must reproduce current behavior exactly.
func TestSqlitePoolConfig(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name            string
		configJSON      string
		wantPoolSize    int
		wantBusyTimeout time.Duration
	}{
		{
			name:            "unset defaults to today's hardcoded values",
			configJSON:      `{}`,
			wantPoolSize:    10,
			wantBusyTimeout: 60 * time.Second,
		},
		{
			name:            "explicit override",
			configJSON:      `{"sqlitePoolSize": 30, "sqliteBusyTimeout": "10s"}`,
			wantPoolSize:    30,
			wantBusyTimeout: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(tempDir, tt.name)
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.configJSON), 0644))

			got, err := LoadConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPoolSize, got.SqlitePoolSize)
			assert.Equal(t, tt.wantBusyTimeout, got.SqliteBusyTimeout)
		})
	}
}

// TestPoolTimeoutConfig verifies the new PoolTimeout knob defaults to today's
// previously-hardcoded value (5s) when unset, and is read correctly from
// config.json when set. PoolTimeout governs how long a caller blocks in
// pool.Take() waiting for a free connection from the SQLite connection pool
// (see docs/features/storage-lock-pool-metrics.md) and is distinct from
// SqliteBusyTimeout, which governs SQLite's own busy-handler on a single
// already-acquired connection.
func TestPoolTimeoutConfig(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name            string
		configJSON      string
		wantPoolTimeout time.Duration
	}{
		{
			name:            "unset defaults to today's hardcoded value",
			configJSON:      `{}`,
			wantPoolTimeout: 15 * time.Second,
		},
		{
			name:            "explicit override",
			configJSON:      `{"poolTimeout": "250ms"}`,
			wantPoolTimeout: 250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(tempDir, tt.name)
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.configJSON), 0644))

			got, err := LoadConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPoolTimeout, got.PoolTimeout)
		})
	}
}

// TestSingleWriterEnabledConfig verifies the SingleWriterEnabled knob defaults to true
// when unset, and is read correctly from config.json when set.
func TestSingleWriterEnabledConfig(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name             string
		configJSON       string
		wantSingleWriter bool
	}{
		{
			name:             "unset defaults to true",
			configJSON:       `{}`,
			wantSingleWriter: true,
		},
		{
			name:             "explicit override false",
			configJSON:       `{"singleWriterEnabled": false}`,
			wantSingleWriter: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(tempDir, tt.name)
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.configJSON), 0644))

			got, err := LoadConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSingleWriter, got.SingleWriterEnabled)
		})
	}
}
