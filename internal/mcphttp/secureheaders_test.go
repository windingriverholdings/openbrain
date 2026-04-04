package mcphttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craig8/openbrain/internal/mcphttp"
	"github.com/stretchr/testify/assert"
)

func TestSecureHeaders_SetsAllHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.SecureHeaders(inner)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestSecureHeaders_PassesThroughRequest(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.SecureHeaders(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called, "inner handler should be called")
	assert.Equal(t, http.StatusOK, rec.Code)
}
