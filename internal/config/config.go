package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBURL                   string
	JWTSecret               string
	Port                    string
	InviteCode              string
	CORSOrigin              string
	AdminUsername           string
	AdminPassword           string
	SentryDSN               string
	PushyAPIKey             string
	SyncSessionTTLDays      int
	DBMaxConns              int32
	DBMinConns              int32
	ReminderThresholdAmount float64
	ReminderDaysOutstanding int
}

func Load() *Config {
	cfg := &Config{
		DBURL:                   getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/finance_manager?sslmode=disable"),
		JWTSecret:               getEnvRequired("JWT_SECRET"),
		Port:                    getEnv("PORT", "8080"),
		InviteCode:              getEnv("INVITE_CODE", ""),
		CORSOrigin:              getEnv("CORS_ORIGIN", "*"),
		AdminUsername:           getEnv("ADMIN_USERNAME", "admin"),
		AdminPassword:           getEnv("ADMIN_PASSWORD", ""),
		SentryDSN:               getEnv("SENTRY_DSN", ""),
		PushyAPIKey:             getEnv("PUSHY_API_KEY", ""),
		SyncSessionTTLDays:      getEnvInt("SYNC_SESSION_TTL_DAYS", 90),
		DBMaxConns:              int32(getEnvInt("DB_MAX_CONNS", 25)),
		DBMinConns:              int32(getEnvInt("DB_MIN_CONNS", 5)),
		ReminderThresholdAmount: getEnvFloat("REMINDER_THRESHOLD_AMOUNT", 20.0),
		ReminderDaysOutstanding: getEnvInt("REMINDER_DAYS_OUTSTANDING", 7),
	}

	// Validate JWT secret strength
	if len(cfg.JWTSecret) < 32 {
		log.Fatal("JWT_SECRET must be at least 32 characters long for security")
	}

	return cfg
}

// Validate checks that the config is safe for the given gin mode ("release", "debug", "test").
// Returns an error listing all violations; in release mode unsafe settings are fatal.
// In non-release mode unsafe settings emit warnings only.
func (c *Config) Validate(ginMode string) error {
	if ginMode != "release" {
		if c.CORSOrigin == "*" {
			log.Println("Warning: CORS_ORIGIN=* — set a specific origin in production")
		}
		if strings.Contains(c.DBURL, "sslmode=disable") {
			log.Println("Warning: DATABASE_URL uses sslmode=disable — enable SSL in production")
		}
		return nil
	}

	// Release mode: fail closed
	var errs []string
	if c.CORSOrigin == "*" {
		errs = append(errs, "CORS_ORIGIN must not be '*' in release mode")
	}
	if strings.Contains(c.DBURL, "sslmode=disable") {
		log.Println("Warning: DATABASE_URL uses sslmode=disable — acceptable for internal/Docker networks, ensure DB is not publicly exposed")
	}
	if c.AdminPassword == "" {
		errs = append(errs, "ADMIN_PASSWORD must be set when admin is enabled in release mode")
	}
	if len(errs) > 0 {
		return fmt.Errorf("unsafe production config: %s", strings.Join(errs, "; "))
	}
	return nil
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

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
		log.Printf("Warning: invalid float for %s, using default %g", key, defaultValue)
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
