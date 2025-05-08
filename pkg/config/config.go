package config

import (
	"time"

	"github.com/spf13/viper"
)

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

	err := viper.ReadInConfig()
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = viper.Unmarshal(&config)
	return config, err
}
