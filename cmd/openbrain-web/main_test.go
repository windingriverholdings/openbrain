package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticAuth is the single gate for every web route (see serveHTTP). These
// tests pin the conditional auth posture Craig selected: an empty WebWSToken
// leaves the surface open with no startup abort, while a set token is required
// via the ?token= query param on every gated route, including the write
// endpoints (/api/capture, /api/ingest). The browser-facing /graph route has
// its own coverage in handler_graph_test.go.

func serveWriteEndpoint(t *testing.T, token, target string) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := staticAuth(token, inner)
	req := httptest.NewRequest(http.MethodPost, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Empty token → the write endpoint runs open (the old fail-closed startup
// abort is gone).
func TestWriteEndpoint_EmptyToken_Open(t *testing.T) {
	rr := serveWriteEndpoint(t, "", "/api/capture")
	require.Equal(t, http.StatusOK, rr.Code)
}

// Set token, missing ?token= → rejected.
func TestWriteEndpoint_SetToken_MissingRejected(t *testing.T) {
	rr := serveWriteEndpoint(t, validToken, "/api/capture")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// Set token, wrong ?token= → rejected.
func TestWriteEndpoint_SetToken_WrongRejected(t *testing.T) {
	rr := serveWriteEndpoint(t, validToken, "/api/capture?token=wrongtoken")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// Set token, correct ?token= → accepted.
func TestWriteEndpoint_SetToken_CorrectAccepted(t *testing.T) {
	rr := serveWriteEndpoint(t, validToken, "/api/capture?token="+validToken)
	require.Equal(t, http.StatusOK, rr.Code)
}

// Static assets (.js, .css, .ico, .png, .svg) bypass staticAuth without needing ?token=.
func TestStaticAuth_StaticAssetsBypassToken(t *testing.T) {
	assets := []string{
		"/composer.js",
		"/composer.css",
		"/detail.js",
		"/detail.css",
		"/favicon.ico",
		"/logo.png",
		"/icon.svg",
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := staticAuth(validToken, inner)

	for _, asset := range assets {
		req := httptest.NewRequest(http.MethodGet, asset, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "static asset %s must bypass auth", asset)
	}
}
