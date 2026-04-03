package mcphttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craig8/openbrain/internal/mcphttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestNewMCPHandler_ReturnsHandler(t *testing.T) {
	handler := mcphttp.NewMCPHandler("test-token", nil, nil)
	require.NotNil(t, handler, "NewMCPHandler should return a non-nil handler")
}

func TestNewSSEHandler_ReturnsHandler(t *testing.T) {
	handler := mcphttp.NewSSEHandler("test-token", nil, nil)
	require.NotNil(t, handler, "NewSSEHandler should return a non-nil handler")
}
