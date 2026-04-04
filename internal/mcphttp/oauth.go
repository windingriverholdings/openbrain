package mcphttp

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// oauthTokenExpiresIn is the number of seconds reported in the token response.
const oauthTokenExpiresIn = 3600

// minOAuthSecretLen is the minimum acceptable length for the OAuth client secret.
const minOAuthSecretLen = 32

// authServerMetadata is the OAuth 2.0 Authorization Server Metadata
// returned by the /.well-known/oauth-authorization-server endpoint.
type authServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

// protectedResourceMetadata is the OAuth Protected Resource metadata
// returned by the /.well-known/oauth-protected-resource endpoint (RFC 9728).
type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// oauthTokenResponse is the successful token response body.
type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// oauthErrorResponse is the standard OAuth 2.0 error response body.
type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// OAuthMetadataHandler returns an http.HandlerFunc that serves the
// OAuth 2.0 Authorization Server Metadata at
// /.well-known/oauth-authorization-server.
func OAuthMetadataHandler(issuer string) http.HandlerFunc {
	meta := authServerMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/authorize",
		TokenEndpoint:                     issuer + "/token",
		RegistrationEndpoint:              issuer + "/register",
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post"},
		GrantTypesSupported:               []string{"authorization_code", "client_credentials"},
		ResponseTypesSupported:            []string{"code"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		panic(fmt.Sprintf("mcphttp.OAuthMetadataHandler: marshal metadata: %v", err))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}
}

// ProtectedResourceHandler returns an http.HandlerFunc that serves the
// OAuth Protected Resource metadata at /.well-known/oauth-protected-resource
// per RFC 9728. This tells MCP clients where to find the authorization server.
func ProtectedResourceHandler(issuer string) http.HandlerFunc {
	meta := protectedResourceMetadata{
		Resource:             issuer,
		AuthorizationServers: []string{issuer},
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		panic(fmt.Sprintf("mcphttp.ProtectedResourceHandler: marshal metadata: %v", err))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}
}

// OAuthTokenHandler returns an http.HandlerFunc that implements the OAuth 2.0
// client_credentials grant. It validates the client_id and client_secret using
// constant-time comparison and returns the configured MCP auth token.
//
// Panics if any parameter is empty — callers must validate before calling.
func OAuthTokenHandler(clientID, clientSecret, authToken string) http.HandlerFunc {
	if clientID == "" {
		panic("mcphttp.OAuthTokenHandler: clientID must not be empty")
	}
	if clientSecret == "" {
		panic("mcphttp.OAuthTokenHandler: clientSecret must not be empty")
	}
	if authToken == "" {
		panic("mcphttp.OAuthTokenHandler: authToken must not be empty")
	}

	clientIDBytes := []byte(clientID)
	clientSecretBytes := []byte(clientSecret)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}

		grantType := r.FormValue("grant_type")
		if grantType != "client_credentials" {
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
				"only client_credentials grant type is supported")
			return
		}

		reqClientID := []byte(r.FormValue("client_id"))
		reqClientSecret := []byte(r.FormValue("client_secret"))

		// Constant-time comparison for both fields. Evaluate both before
		// short-circuiting to avoid timing leaks on which field was wrong.
		idMatch := subtle.ConstantTimeCompare(reqClientID, clientIDBytes)
		secretMatch := subtle.ConstantTimeCompare(reqClientSecret, clientSecretBytes)

		if idMatch&secretMatch != 1 {
			slog.Warn("OAuth token request: invalid credentials",
				"remote_addr", extractIP(r))
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
				"client authentication failed")
			return
		}

		resp := oauthTokenResponse{
			AccessToken: authToken,
			TokenType:   "bearer",
			ExpiresIn:   oauthTokenExpiresIn,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// writeOAuthError writes a standard OAuth 2.0 error response.
func writeOAuthError(w http.ResponseWriter, status int, errorCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(oauthErrorResponse{
		Error:            errorCode,
		ErrorDescription: description,
	})
}

// ValidateOAuthConfig checks that OAuth configuration is complete when
// MCP HTTP is enabled. Returns nil if disabled or if config is valid.
func ValidateOAuthConfig(mcpHTTPEnabled bool, clientID, clientSecret string) error {
	if !mcpHTTPEnabled {
		return nil
	}
	if clientID == "" && clientSecret == "" {
		// OAuth is optional — if neither is set, skip validation.
		return nil
	}
	if clientID == "" {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_ID is required when OPENBRAIN_OAUTH_CLIENT_SECRET is set")
	}
	if clientSecret == "" {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_SECRET is required when OPENBRAIN_OAUTH_CLIENT_ID is set")
	}
	if len(clientSecret) < minOAuthSecretLen {
		return fmt.Errorf("OPENBRAIN_OAUTH_CLIENT_SECRET must be at least %d characters (got %d)", minOAuthSecretLen, len(clientSecret))
	}
	return nil
}
