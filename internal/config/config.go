package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	DBURL              string
	JWTSecret          string
	Port               string
	InviteCode         string
	CORSOrigin         string
	AdminUsername       string
	AdminPassword      string
	SyncSessionTTLDays int
}

func Load() *Config {
	cfg := &Config{
		DBURL:      getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/finance_manager?sslmode=disable"),
		JWTSecret:  getEnvRequired("JWT_SECRET"),
		Port:       getEnv("PORT", "8080"),
		InviteCode:    getEnv("INVITE_CODE", ""),
		CORSOrigin:    getEnv("CORS_ORIGIN", "*"),
		AdminUsername:       getEnv("ADMIN_USERNAME", "admin"),
		AdminPassword:       getEnv("ADMIN_PASSWORD", ""),
		SyncSessionTTLDays: getEnvInt("SYNC_SESSION_TTL_DAYS", 90),
	}

	// Validate JWT secret strength
	if len(cfg.JWTSecret) < 32 {
		log.Fatal("JWT_SECRET must be at least 32 characters long for security")
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
		log.Printf("Warning: invalid integer for %s, using default %d", key, defaultValue)
	}
	return defaultValue
}

func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return value
}
