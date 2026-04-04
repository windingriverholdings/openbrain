package mcphttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/craig8/openbrain/internal/mcphttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIssuer       = "https://openbrain.test"
	testClientID     = "test-client-id-1234567890"
	testClientSecret = "test-client-secret-min-32-chars-long-enough"
	testAuthToken    = "test-mcp-auth-token-min-32-chars-long-enough"
)

// --- OAuthMetadataHandler tests ---

func TestOAuthMetadataHandler_ReturnsCorrectJSON(t *testing.T) {
	handler := mcphttp.OAuthMetadataHandler(testIssuer)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var meta map[string]any
	err := json.NewDecoder(rec.Body).Decode(&meta)
	require.NoError(t, err)

	assert.Equal(t, testIssuer, meta["issuer"])
	assert.Equal(t, testIssuer+"/oauth/token", meta["token_endpoint"])
	assert.Contains(t, meta["grant_types_supported"], "client_credentials")
	assert.Contains(t, meta["token_endpoint_auth_methods_supported"], "client_secret_post")
	assert.Contains(t, meta["response_types_supported"], "token")
}

func TestOAuthMetadataHandler_RejectsNonGET(t *testing.T) {
	handler := mcphttp.OAuthMetadataHandler(testIssuer)

	req := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- ProtectedResourceHandler tests ---

func TestProtectedResourceHandler_ReturnsCorrectJSON(t *testing.T) {
	handler := mcphttp.ProtectedResourceHandler(testIssuer)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var meta map[string]any
	err := json.NewDecoder(rec.Body).Decode(&meta)
	require.NoError(t, err)

	assert.Equal(t, testIssuer, meta["resource"])
	servers, ok := meta["authorization_servers"].([]any)
	require.True(t, ok)
	assert.Contains(t, servers, testIssuer)
}

func TestProtectedResourceHandler_RejectsNonGET(t *testing.T) {
	handler := mcphttp.ProtectedResourceHandler(testIssuer)

	req := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- OAuthTokenHandler tests ---

func TestOAuthTokenHandler_ValidCredentials(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", testClientID)
	form.Set("client_secret", testClientSecret)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, testAuthToken, body["access_token"])
	assert.Equal(t, "bearer", body["token_type"])
	assert.Equal(t, float64(3600), body["expires_in"])
}

func TestOAuthTokenHandler_InvalidClientID(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "wrong-client")
	form.Set("client_secret", testClientSecret)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_client", body["error"])
}

func TestOAuthTokenHandler_InvalidClientSecret(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", testClientID)
	form.Set("client_secret", "wrong-secret")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_client", body["error"])
}

func TestOAuthTokenHandler_MissingGrantType(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("client_id", testClientID)
	form.Set("client_secret", testClientSecret)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "unsupported_grant_type", body["error"])
}

func TestOAuthTokenHandler_WrongGrantType(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", testClientID)
	form.Set("client_secret", testClientSecret)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "unsupported_grant_type", body["error"])
}

func TestOAuthTokenHandler_RejectsNonPOST(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	req := httptest.NewRequest(http.MethodGet, "/oauth/token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestOAuthTokenHandler_MissingClientID(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_secret", testClientSecret)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_client", body["error"])
}

func TestOAuthTokenHandler_MissingClientSecret(t *testing.T) {
	handler := mcphttp.OAuthTokenHandler(testClientID, testClientSecret, testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", testClientID)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_client", body["error"])
}

// --- Config validation tests ---

func TestValidateOAuthConfig_ValidWhenDisabled(t *testing.T) {
	err := mcphttp.ValidateOAuthConfig(false, "", "")
	assert.NoError(t, err)
}

func TestValidateOAuthConfig_RequiresBothWhenEnabled(t *testing.T) {
	err := mcphttp.ValidateOAuthConfig(true, testClientID, "")
	assert.Error(t, err)

	err = mcphttp.ValidateOAuthConfig(true, "", testClientSecret)
	assert.Error(t, err)
}

func TestValidateOAuthConfig_SecretMinLength(t *testing.T) {
	err := mcphttp.ValidateOAuthConfig(true, testClientID, "short")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestValidateOAuthConfig_ValidWhenComplete(t *testing.T) {
	err := mcphttp.ValidateOAuthConfig(true, testClientID, testClientSecret)
	assert.NoError(t, err)
}

// --- Constructor panics ---

func TestOAuthTokenHandler_PanicsOnEmptyClientID(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.OAuthTokenHandler("", testClientSecret, testAuthToken)
	})
}

func TestOAuthTokenHandler_PanicsOnEmptyClientSecret(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.OAuthTokenHandler(testClientID, "", testAuthToken)
	})
}

func TestOAuthTokenHandler_PanicsOnEmptyAuthToken(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.OAuthTokenHandler(testClientID, testClientSecret, "")
	})
}
