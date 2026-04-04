package mcphttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craig8/openbrain/internal/mcphttp"
	"github.com/stretchr/testify/assert"
)

func TestRateLimit_AllowsBurst(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Allow 1 req/sec with burst of 5
	handler := mcphttp.RateLimit(1, 5, inner)

	// First 5 requests should succeed (burst)
	for i := range 5 {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "request %d should succeed", i+1)
	}

	// 6th request should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestRateLimit_DifferentIPsIndependent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.RateLimit(1, 2, inner)

	// Exhaust IP1's burst
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.RemoteAddr = "10.0.0.1:1111"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}

	// IP1 should be limited
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "10.0.0.1:1111"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)

	// IP2 should still succeed
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req2.RemoteAddr = "10.0.0.2:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

func TestRateLimit_UsesXForwardedFor(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.RateLimit(1, 1, inner)

	// First request with X-Forwarded-For
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "10.0.0.1:1111"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 70.41.3.18")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Second request from same X-Forwarded-For should be limited
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req2.RemoteAddr = "10.0.0.1:1111"
	req2.Header.Set("X-Forwarded-For", "203.0.113.5, 70.41.3.18")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code)
}
