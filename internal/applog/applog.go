// Package applog centralises structured logging using log/slog.
//
// It exposes:
//   - Init: configure the default *slog.Logger from a level string ("debug",
//     "info", "warn", "error"). Call once at startup, before any logging.
//   - ParseLevel: convert a string level to slog.Level.
//   - From: pull the per-request logger out of *gin.Context, falling back to
//     slog.Default() outside of a request scope.
//   - With: store a *slog.Logger on *gin.Context (used by middleware to
//     attach request_id, user_id, etc.).
//
// Standardised attribute keys are exported as constants so every package
// emits the same field names (request_id, user_id, group_id, ...).
package applog

import (
	"log/slog"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// ContextKey is the gin.Context key used to stash the per-request logger.
const ContextKey = "applog.logger"

// Standard attribute keys. Use these constants instead of stringly-typed
// keys so every package emits the same field names.
const (
	KeyRequestID    = "request_id"
	KeyUserID       = "user_id"
	KeyGroupID      = "group_id"
	KeySettlementID = "settlement_id"
	KeyDurationMS   = "duration_ms"
	KeyError        = "error"
)

// Init replaces slog's default logger with a text handler at the parsed level.
// Output is stderr to match the stdlib log package's default.
func Init(level string) *slog.Logger {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: ParseLevel(level),
	})
	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger
}

// ParseLevel converts a textual level to slog.Level. Unknown values fall
// back to slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// From returns the per-request *slog.Logger stored on the gin.Context, or
// slog.Default() if no logger has been attached. Safe to call with a nil
// context.
func From(c *gin.Context) *slog.Logger {
	if c == nil {
		return slog.Default()
	}
	v, ok := c.Get(ContextKey)
	if !ok {
		return slog.Default()
	}
	logger, ok := v.(*slog.Logger)
	if !ok || logger == nil {
		return slog.Default()
	}
	return logger
}

// With stores logger on the gin.Context so subsequent applog.From(c) calls
// pick it up. Used by middleware to enrich the per-request logger with
// fields like user_id once authentication completes.
func With(c *gin.Context, logger *slog.Logger) {
	if c == nil || logger == nil {
		return
	}
	c.Set(ContextKey, logger)
}
