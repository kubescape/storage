package config

import (
	"time"

	"github.com/spf13/viper"
)

type KindQueueConfig struct {
	QueueLength   int `mapstructure:"queueLength"`
	WorkerCount   int `mapstructure:"workerCount"`
	MaxObjectSize int `mapstructure:"maxObjectSize"`
}

type Config struct {
	CleanupInterval            time.Duration `mapstructure:"cleanupInterval"`
	DefaultNamespace           string        `mapstructure:"defaultNamespace"`
	DisableVirtualCRDs         bool          `mapstructure:"disableVirtualCRDs"`
	ExcludeJsonPaths           []string      `mapstructure:"excludeJsonPaths"`
	MaxApplicationProfileSize  int           `mapstructure:"maxApplicationProfileSize"`
	MaxNetworkNeighborhoodSize int           `mapstructure:"maxNetworkNeighborhoodSize"`
	RateLimitPerClient         float64       `mapstructure:"rateLimitPerClient"`
	RateLimitTotal             int           `mapstructure:"rateLimitTotal"`
	ServerBindPort             int           `mapstructure:"serverBindPort"`
	TlsClientCaFile            string        `mapstructure:"tlsClientCaFile"`
	TlsServerCertFile          string        `mapstructure:"tlsServerCertFile"`
	TlsServerKeyFile           string        `mapstructure:"tlsServerKeyFile"`

	// New fields for per-kind queue/worker/object size config
	KindQueues           map[string]KindQueueConfig `mapstructure:"kindQueues"`
	DefaultQueueLength   int                        `mapstructure:"defaultQueueLength"`
	DefaultWorkerCount   int                        `mapstructure:"defaultWorkerCount"`
	DefaultMaxObjectSize int                        `mapstructure:"defaultMaxObjectSize"`

	// Debugging
	QueueTimeoutPrint         bool `mapstructure:"queueTimeoutPrint"`
	QueueTimeout              int  `mapstructure:"queueTimeout"`
	QueueProcessingStatsPrint bool `mapstructure:"queueProcessingStatsPrint"`
}

// LoadConfig reads configuration from file or environment variables.
func LoadConfig(path string) (Config, error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("config")
	viper.SetConfigType("json")

	viper.SetDefault("cleanupInterval", 24*time.Hour)
	viper.SetDefault("defaultNamespace", "kubescape")
	viper.SetDefault("maxApplicationProfileSize", 40000)
	viper.SetDefault("maxNetworkNeighborhoodSize", 40000)
	viper.SetDefault("rateLimitTotal", 10)
	viper.SetDefault("serverBindPort", 8443)
	viper.SetDefault("defaultQueueLength", 100)
	viper.SetDefault("defaultWorkerCount", 2)
	viper.SetDefault("defaultMaxObjectSize", 400000)
	viper.SetDefault("queueTimeoutPrint", false)
	viper.SetDefault("queueTimeout", 60)
	viper.SetDefault("queueProcessingStatsPrint", false)
	viper.SetDefault("kindQueues", map[string]KindQueueConfig{
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
	})

	err := viper.ReadInConfig()
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = viper.Unmarshal(&config)
	return config, err
}
