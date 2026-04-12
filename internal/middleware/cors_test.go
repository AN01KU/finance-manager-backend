package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestCORS(t *testing.T) {
	tests := []struct {
		name            string
		allowedOrigin   string
		method          string
		wantOrigin      string
		wantCredentials string
		wantStatus      int
	}{
		{
			name:            "wildcard origin",
			allowedOrigin:   "*",
			method:          "GET",
			wantOrigin:      "*",
			wantCredentials: "",
			wantStatus:      200,
		},
		{
			name:            "specific origin sets credentials",
			allowedOrigin:   "https://example.com",
			method:          "GET",
			wantOrigin:      "https://example.com",
			wantCredentials: "true",
			wantStatus:      200,
		},
		{
			name:            "empty origin defaults to wildcard",
			allowedOrigin:   "",
			method:          "GET",
			wantOrigin:      "*",
			wantCredentials: "",
			wantStatus:      200,
		},
		{
			name:            "OPTIONS preflight returns 204",
			allowedOrigin:   "https://example.com",
			method:          "OPTIONS",
			wantOrigin:      "https://example.com",
			wantCredentials: "true",
			wantStatus:      204,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(CORS(tt.allowedOrigin))
			r.GET("/test", func(c *gin.Context) { c.Status(200) })

			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, tt.wantOrigin, w.Header().Get("Access-Control-Allow-Origin"))

			cred := w.Header().Get("Access-Control-Allow-Credentials")
			assert.Equal(t, tt.wantCredentials, cred)

			assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
		})
	}
}
