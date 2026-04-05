package config

import (
	"log"
	"os"
)

type Config struct {
	DBURL         string
	JWTSecret     string
	Port          string
	InviteCode    string
	AdminUsername  string
	AdminPassword string
}

func Load() *Config {
	cfg := &Config{
		DBURL:      getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/finance_manager?sslmode=disable"),
		JWTSecret:  getEnvRequired("JWT_SECRET"),
		Port:       getEnv("PORT", "8080"),
		InviteCode:    getEnv("INVITE_CODE", ""),
		AdminUsername:  getEnv("ADMIN_USERNAME", "admin"),
		AdminPassword:  getEnv("ADMIN_PASSWORD", ""),
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

func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return value
}
