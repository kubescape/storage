package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/stretchr/testify/assert"
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
				CleanupInterval:            24 * time.Hour,
				DefaultNamespace:           "kubescape",
				HostType:                   armotypes.HostTypeKubernetes,
				ExcludeJsonPaths:           []string{".containers[*].env[?(@.name==\"KUBECONFIG\")]"},
				MaxApplicationProfileSize:  40000,
				MaxNetworkNeighborhoodSize: 40000,
				RateLimitTotal:             10,
				ServerBindPort:             8443,
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
