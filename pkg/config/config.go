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
	MaxApplicationProfileSize     int                `mapstructure:"maxApplicationProfileSize"`
	MaxNetworkNeighborhoodSize    int                `mapstructure:"maxNetworkNeighborhoodSize"`
	MaxSniffingTime               time.Duration      `mapstructure:"maxSniffingTimePerContainer"`
	RateLimitPerClient            float64            `mapstructure:"rateLimitPerClient"`
	RateLimitTotal                int                `mapstructure:"rateLimitTotal"`
	ServerBindPort                int                `mapstructure:"serverBindPort"`
	TlsClientCaFile               string             `mapstructure:"tlsClientCaFile"`
	TlsServerCertFile             string             `mapstructure:"tlsServerCertFile"`
	TlsServerKeyFile              string             `mapstructure:"tlsServerKeyFile"`

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
	v.SetDefault("maxApplicationProfileSize", 40000)
	v.SetDefault("maxNetworkNeighborhoodSize", 40000)
	v.SetDefault("rateLimitTotal", 10)
	v.SetDefault("serverBindPort", 8443)
	v.SetDefault("defaultQueueLength", 100)
	v.SetDefault("defaultWorkerCount", 2)
	v.SetDefault("defaultMaxObjectSize", 400000)
	v.SetDefault("queueManagerEnabled", false)
	v.SetDefault("queueTimeoutPrint", false)
	v.SetDefault("queueTimeout", 60)
	v.SetDefault("queueProcessingStatsPrint", false)
	v.SetDefault("kindQueues", map[string]KindQueueConfig{
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

	switch string(config.HostType) {
	case "kubernetes", "ecs-ec2", "ecs-fargate", "aks", "aci", "azurevm", "cloudrun", "autopilot":
		// valid
	default:
		return Config{}, fmt.Errorf("unsupported hostType: %s", config.HostType)
	}

	return config, nil
}
