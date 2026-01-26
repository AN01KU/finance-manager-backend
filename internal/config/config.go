package config

import (
	"os"
)

type Config struct {
	DBURL     string
	JWTSecret string
	Port      string
}

func Load() *Config {
	return &Config{
		DBURL:     getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/finance_manager?sslmode=disable"),
		JWTSecret: getEnv("JWT_SECRET", "your-secret-key"),
		Port:      getEnv("PORT", "8080"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
