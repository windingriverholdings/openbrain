package mcphttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craig8/openbrain/internal/mcphttp"
	"github.com/stretchr/testify/assert"
)

func TestBearerAuthMiddleware_RejectsUnauthenticated(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.BearerAuth("test-secret-token", inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBearerAuthMiddleware_RejectsWrongToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.BearerAuth("test-secret-token", inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBearerAuthMiddleware_AcceptsCorrectToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.BearerAuth("test-secret-token", inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer test-secret-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBearerAuthMiddleware_RejectsMalformedHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := mcphttp.BearerAuth("test-secret-token", inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewMCPHandler_PanicsOnNilBrain(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.NewMCPHandler("test-token", "openbrain", "0.1.0", nil, nil)
	}, "NewMCPHandler should panic when brain is nil")
}

func TestNewSSEHandler_PanicsOnNilBrain(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.NewSSEHandler("test-token", "openbrain", "0.1.0", nil, nil)
	}, "NewSSEHandler should panic when brain is nil")
}

func TestBearerAuth_PanicsOnEmptyToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	assert.Panics(t, func() {
		mcphttp.BearerAuth("", inner)
	}, "BearerAuth should panic when token is empty")
}
