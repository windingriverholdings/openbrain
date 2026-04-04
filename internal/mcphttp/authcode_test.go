package mcphttp_test

import (
	"crypto/sha256"
	"encoding/base64"
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

// --- Dynamic Client Registration tests ---

func TestRegisterHandler_ValidRegistration(t *testing.T) {
	handler := mcphttp.RegisterHandler()

	body := `{"client_name":"test-client","redirect_uris":["https://example.com/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	// Must return a client_id
	clientID, ok := resp["client_id"].(string)
	assert.True(t, ok, "response must contain client_id string")
	assert.NotEmpty(t, clientID)

	// Must echo back client_name and redirect_uris
	assert.Equal(t, "test-client", resp["client_name"])

	redirectURIs, ok := resp["redirect_uris"].([]any)
	require.True(t, ok)
	assert.Contains(t, redirectURIs, "https://example.com/callback")

	// Must include token_endpoint_auth_method
	assert.Equal(t, "none", resp["token_endpoint_auth_method"])
}

func TestRegisterHandler_MinimalRegistration(t *testing.T) {
	handler := mcphttp.RegisterHandler()

	// No client_name, just redirect_uris
	body := `{"redirect_uris":["https://example.com/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["client_id"])
}

func TestRegisterHandler_RejectsNonPOST(t *testing.T) {
	handler := mcphttp.RegisterHandler()

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRegisterHandler_RejectsInvalidJSON(t *testing.T) {
	handler := mcphttp.RegisterHandler()

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterHandler_UniqueClientIDs(t *testing.T) {
	handler := mcphttp.RegisterHandler()

	ids := make(map[string]bool)
	for range 5 {
		body := `{"redirect_uris":["https://example.com/callback"]}`
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		var resp map[string]any
		err := json.NewDecoder(rec.Body).Decode(&resp)
		require.NoError(t, err)

		clientID := resp["client_id"].(string)
		assert.False(t, ids[clientID], "client_id should be unique")
		ids[clientID] = true
	}
}

// --- Authorize endpoint tests ---

func TestAuthorizeHandler_RedirectsWithCode(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	challenge := generateCodeChallenge("test-verifier-1234567890")

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "some-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("state", "random-state-value")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	require.NotEmpty(t, location, "must redirect")

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Must redirect to the redirect_uri
	assert.Equal(t, "https", redirectURL.Scheme)
	assert.Equal(t, "example.com", redirectURL.Host)
	assert.Equal(t, "/callback", redirectURL.Path)

	// Must include state
	assert.Equal(t, "random-state-value", redirectURL.Query().Get("state"))

	// Must include authorization code
	code := redirectURL.Query().Get("code")
	assert.NotEmpty(t, code, "must include authorization code")
}

func TestAuthorizeHandler_MissingResponseType(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	params := url.Values{}
	params.Set("client_id", "some-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("code_challenge", "abc")
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthorizeHandler_UnsupportedResponseType(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	params := url.Values{}
	params.Set("response_type", "token")
	params.Set("client_id", "some-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("code_challenge", "abc")
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthorizeHandler_MissingClientID(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("code_challenge", "abc")
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthorizeHandler_MissingRedirectURI(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "some-client")
	params.Set("code_challenge", "abc")
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthorizeHandler_MissingCodeChallenge(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "some-client")
	params.Set("redirect_uri", "https://example.com/callback")

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthorizeHandler_RejectsNonGET(t *testing.T) {
	handler := mcphttp.AuthorizeHandler()

	req := httptest.NewRequest(http.MethodPost, "/authorize", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- Token endpoint with authorization_code grant ---

func TestTokenHandler_AuthorizationCodeGrant(t *testing.T) {
	authHandler := mcphttp.AuthorizeHandler()
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	// Step 1: Get an authorization code via /authorize
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := generateCodeChallenge(verifier)

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "test-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("state", "test-state")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)

	require.Equal(t, http.StatusFound, authRec.Code)

	location := authRec.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	code := redirectURL.Query().Get("code")
	require.NotEmpty(t, code)

	// Step 2: Exchange the code for a token
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "https://example.com/callback")
	form.Set("code_verifier", verifier)
	form.Set("client_id", "test-client")

	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(tokenRec, tokenReq)

	assert.Equal(t, http.StatusOK, tokenRec.Code)
	assert.Equal(t, "application/json", tokenRec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", tokenRec.Header().Get("Cache-Control"))

	var body map[string]any
	err = json.NewDecoder(tokenRec.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, testAuthToken, body["access_token"])
	assert.Equal(t, "bearer", body["token_type"])
	assert.Equal(t, float64(3600), body["expires_in"])
}

func TestTokenHandler_AuthorizationCodeGrant_InvalidCode(t *testing.T) {
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "invalid-code")
	form.Set("redirect_uri", "https://example.com/callback")
	form.Set("code_verifier", "some-verifier")
	form.Set("client_id", "test-client")

	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_grant", body["error"])
}

func TestTokenHandler_AuthorizationCodeGrant_WrongVerifier(t *testing.T) {
	authHandler := mcphttp.AuthorizeHandler()
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	// Get a code with one challenge
	verifier := "correct-verifier-that-is-long-enough"
	challenge := generateCodeChallenge(verifier)

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "test-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("state", "s")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)

	location := authRec.Header().Get("Location")
	redirectURL, _ := url.Parse(location)
	code := redirectURL.Query().Get("code")

	// Try to exchange with WRONG verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "https://example.com/callback")
	form.Set("code_verifier", "wrong-verifier-does-not-match")
	form.Set("client_id", "test-client")

	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(tokenRec, tokenReq)

	assert.Equal(t, http.StatusBadRequest, tokenRec.Code)

	var body map[string]any
	err := json.NewDecoder(tokenRec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_grant", body["error"])
}

func TestTokenHandler_AuthorizationCodeGrant_CodeReuse(t *testing.T) {
	authHandler := mcphttp.AuthorizeHandler()
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := generateCodeChallenge(verifier)

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "test-client")
	params.Set("redirect_uri", "https://example.com/callback")
	params.Set("state", "s")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+params.Encode(), nil)
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)

	location := authRec.Header().Get("Location")
	redirectURL, _ := url.Parse(location)
	code := redirectURL.Query().Get("code")

	// First exchange should succeed
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "https://example.com/callback")
	form.Set("code_verifier", verifier)
	form.Set("client_id", "test-client")

	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(tokenRec, tokenReq)
	assert.Equal(t, http.StatusOK, tokenRec.Code)

	// Second exchange with same code should fail
	tokenReq2 := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokenReq2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec2 := httptest.NewRecorder()
	tokenHandler.ServeHTTP(tokenRec2, tokenReq2)
	assert.Equal(t, http.StatusBadRequest, tokenRec2.Code)
}

func TestTokenHandler_AuthorizationCodeGrant_MissingFields(t *testing.T) {
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	tests := []struct {
		name string
		form url.Values
	}{
		{
			name: "missing code",
			form: func() url.Values {
				f := url.Values{}
				f.Set("grant_type", "authorization_code")
				f.Set("redirect_uri", "https://example.com/callback")
				f.Set("code_verifier", "verifier")
				f.Set("client_id", "client")
				return f
			}(),
		},
		{
			name: "missing code_verifier",
			form: func() url.Values {
				f := url.Values{}
				f.Set("grant_type", "authorization_code")
				f.Set("code", "some-code")
				f.Set("redirect_uri", "https://example.com/callback")
				f.Set("client_id", "client")
				return f
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			tokenHandler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestTokenHandler_RejectsNonPOST(t *testing.T) {
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	rec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestTokenHandler_UnsupportedGrantType(t *testing.T) {
	tokenHandler := mcphttp.AuthCodeTokenHandler(testAuthToken)

	form := url.Values{}
	form.Set("grant_type", "implicit")

	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	tokenHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "unsupported_grant_type", body["error"])
}

func TestAuthCodeTokenHandler_PanicsOnEmptyToken(t *testing.T) {
	assert.Panics(t, func() {
		mcphttp.AuthCodeTokenHandler("")
	})
}

// --- Updated metadata tests ---

func TestOAuthMetadataHandler_IncludesAuthorizationEndpoint(t *testing.T) {
	handler := mcphttp.OAuthMetadataHandler(testIssuer)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var meta map[string]any
	err := json.NewDecoder(rec.Body).Decode(&meta)
	require.NoError(t, err)

	// Must include authorization_endpoint for MCP spec compliance
	assert.Equal(t, testIssuer+"/authorize", meta["authorization_endpoint"])

	// Must include token_endpoint
	assert.Equal(t, testIssuer+"/token", meta["token_endpoint"])

	// Must include registration_endpoint
	assert.Equal(t, testIssuer+"/register", meta["registration_endpoint"])

	// Must include code in response_types_supported
	assert.Contains(t, meta["response_types_supported"], "code")

	// Must include authorization_code in grant_types_supported
	grantTypes := meta["grant_types_supported"].([]any)
	assert.Contains(t, grantTypes, "authorization_code")

	// Must include code_challenge_methods_supported with S256
	challengeMethods := meta["code_challenge_methods_supported"].([]any)
	assert.Contains(t, challengeMethods, "S256")
}

// --- helper ---

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
