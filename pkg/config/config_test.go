package config

import (
	"testing"
	"time"

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
				ExcludeJsonPaths:           []string{".containers[*].env[?(@.name==\"KUBECONFIG\")]"},
				MaxApplicationProfileSize:  40000,
				MaxNetworkNeighborhoodSize: 40000,
				RateLimitTotal:             10,
				ServerBindPort:             8443,
				KindQueues: map[string]KindQueueConfig{
					"applicationprofiles": {
						QueueLength:   50,
						WorkerCount:   1,
						MaxObjectSize: 20000000,
					},
					"containerprofiles": {
						QueueLength:   50,
						WorkerCount:   1,
						MaxObjectSize: 2500000,
					},
					"networkneighborhoods": {
						QueueLength:   50,
						WorkerCount:   1,
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
