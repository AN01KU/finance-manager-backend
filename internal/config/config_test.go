package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func safeConfig() *Config {
	return &Config{
		DBURL:         "postgres://user:password@localhost:5432/finance_manager?sslmode=require",
		JWTSecret:     "supersecretjwtsecretthatis32chars!!",
		Port:          "8080",
		CORSOrigin:    "https://example.com",
		AdminUsername: "admin",
		AdminPassword: "s3cr3t",
	}
}

func TestValidate_ReleaseMode(t *testing.T) {
	t.Run("valid release config passes", func(t *testing.T) {
		cfg := safeConfig()
		err := cfg.Validate("release")
		require.NoError(t, err)
	})

	t.Run("fatal if CORS_ORIGIN is wildcard in release", func(t *testing.T) {
		cfg := safeConfig()
		cfg.CORSOrigin = "*"
		err := cfg.Validate("release")
		assert.ErrorContains(t, err, "CORS_ORIGIN")
	})

	t.Run("sslmode=disable allowed in release for internal/Docker networks", func(t *testing.T) {
		cfg := safeConfig()
		cfg.DBURL = "postgres://user:password@localhost:5432/finance_manager?sslmode=disable"
		err := cfg.Validate("release")
		assert.NoError(t, err)
	})

	t.Run("fatal if admin password is empty when admin username is set in release", func(t *testing.T) {
		cfg := safeConfig()
		cfg.AdminPassword = ""
		err := cfg.Validate("release")
		assert.ErrorContains(t, err, "ADMIN_PASSWORD")
	})
}

func TestValidate_NonReleaseMode(t *testing.T) {
	t.Run("wildcard CORS allowed in non-release", func(t *testing.T) {
		cfg := &Config{
			DBURL:      "postgres://user:password@localhost:5432/finance_manager?sslmode=disable",
			JWTSecret:  "supersecretjwtsecretthatis32chars!!",
			Port:       "8080",
			CORSOrigin: "*",
		}
		err := cfg.Validate("debug")
		require.NoError(t, err)
	})
}
