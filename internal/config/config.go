package config

import (
	"fmt"
	"github.com/spf13/viper"
)

type Config struct {
	RedisURL                      string
	RedisReplicaURL               string
	DatabaseURL                   string
	ServerPort                    string
	ReservationTTLSeconds         int
	RateLimitCapacity             float64
	RateLimitRatePerSec           float64
	ReconciliationIntervalSeconds int
}

func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	viper.SetDefault("REDIS_URL", "redis://localhost:6379")
	viper.SetDefault("REDIS_REPLICA_URL", "")
	viper.SetDefault("DATABASE_URL", "postgres://user:pass@localhost:5432/flashsale")
	viper.SetDefault("SERVER_PORT", "8080")
	viper.SetDefault("RESERVATION_TTL_SECONDS", 600)
	viper.SetDefault("RATE_LIMIT_CAPACITY", 10)
	viper.SetDefault("RATE_LIMIT_RATE_PER_SEC", 2)
	viper.SetDefault("RECONCILIATION_INTERVAL_SECONDS", 60)

	cfg := &Config{
		RedisURL:                      viper.GetString("REDIS_URL"),
		RedisReplicaURL:               viper.GetString("REDIS_REPLICA_URL"),
		DatabaseURL:                   viper.GetString("DATABASE_URL"),
		ServerPort:                    viper.GetString("SERVER_PORT"),
		ReservationTTLSeconds:         viper.GetInt("RESERVATION_TTL_SECONDS"),
		RateLimitCapacity:             viper.GetFloat64("RATE_LIMIT_CAPACITY"),
		RateLimitRatePerSec:           viper.GetFloat64("RATE_LIMIT_RATE_PER_SEC"),
		ReconciliationIntervalSeconds: viper.GetInt("RECONCILIATION_INTERVAL_SECONDS"),
	}

	return cfg, nil
}
