package applog

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"  debug  ", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"trace", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLevel(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFrom_NilContext(t *testing.T) {
	logger := From(nil)
	require.NotNil(t, logger)
	assert.Equal(t, slog.Default(), logger)
}

func TestFrom_NoLoggerAttached(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	logger := From(c)
	require.NotNil(t, logger)
	assert.Equal(t, slog.Default(), logger)
}

func TestFrom_WrongTypeInContext(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ContextKey, "not-a-logger")
	logger := From(c)
	require.NotNil(t, logger)
	assert.Equal(t, slog.Default(), logger)
}

func TestWith_And_From_RoundTrip(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	custom := slog.Default().With("request_id", "test-123")
	With(c, custom)

	got := From(c)
	require.NotNil(t, got)
	assert.Equal(t, custom, got)
}

func TestWith_NilContextNoPanic(t *testing.T) {
	require.NotPanics(t, func() {
		With(nil, slog.Default())
	})
}

func TestWith_NilLoggerNoPanic(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	require.NotPanics(t, func() {
		With(c, nil)
	})
	// Falls back to default since nil was rejected.
	assert.Equal(t, slog.Default(), From(c))
}
