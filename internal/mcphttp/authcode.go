package mcphttp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// authCode holds a pending authorization code and its associated PKCE challenge.
type authCode struct {
	codeChallenge string
	clientID      string
	redirectURI   string
	createdAt     time.Time
}

// authCodeTTL is the maximum lifetime of an authorization code.
const authCodeTTL = 10 * time.Minute

// codeStore is an in-memory store for pending authorization codes.
// Codes are single-use and expire after authCodeTTL.
var codeStore = &authCodeStore{
	codes: make(map[string]authCode),
}

type authCodeStore struct {
	mu    sync.Mutex
	codes map[string]authCode
}

// put stores an authorization code. Returns the generated code string.
func (s *authCodeStore) put(challenge, clientID, redirectURI string) string {
	code := generateRandomCode()
	s.mu.Lock()
	s.codes[code] = authCode{
		codeChallenge: challenge,
		clientID:      clientID,
		redirectURI:   redirectURI,
		createdAt:     time.Now(),
	}
	s.mu.Unlock()
	return code
}

// consume retrieves and removes an authorization code. Returns the authCode
// and true if found and not expired, or zero value and false otherwise.
func (s *authCodeStore) consume(code string) (authCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ac, exists := s.codes[code]
	if !exists {
		return authCode{}, false
	}
	delete(s.codes, code)

	if time.Since(ac.createdAt) > authCodeTTL {
		return authCode{}, false
	}
	return ac, true
}

// generateRandomCode produces a cryptographically random hex string.
func generateRandomCode() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("mcphttp: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// clientRegistration holds the response data for dynamic client registration.
type clientRegistration struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// registrationRequest is the expected JSON body for POST /register.
type registrationRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// RegisterHandler returns an http.HandlerFunc that implements OAuth 2.0
// Dynamic Client Registration (RFC 7591). Since this is a single-user
// machine-to-machine server, registration always succeeds and returns
// a unique client_id. The client_id is not validated on subsequent
// requests — any registered client can authorize.
func RegisterHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req registrationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata",
				"malformed JSON body")
			return
		}

		clientID := generateRandomCode()

		resp := clientRegistration{
			ClientID:                clientID,
			ClientName:              req.ClientName,
			RedirectURIs:            req.RedirectURIs,
			TokenEndpointAuthMethod: "none",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}

// AuthorizeHandler returns an http.HandlerFunc that implements the OAuth 2.0
// authorization endpoint for the authorization code grant with PKCE.
//
// Since OpenBrain is a single-user, machine-to-machine MCP server, this
// endpoint auto-approves all authorization requests. There is no login or
// consent screen. It generates an authorization code, stores the PKCE
// challenge, and redirects back to the client's redirect_uri.
func AuthorizeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		q := r.URL.Query()

		responseType := q.Get("response_type")
		if responseType == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing response_type parameter")
			return
		}
		if responseType != "code" {
			writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type",
				"only response_type=code is supported")
			return
		}

		clientID := q.Get("client_id")
		if clientID == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing client_id parameter")
			return
		}

		redirectURI := q.Get("redirect_uri")
		if redirectURI == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing redirect_uri parameter")
			return
		}

		codeChallenge := q.Get("code_challenge")
		if codeChallenge == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing code_challenge parameter (PKCE required)")
			return
		}

		codeChallengeMethod := q.Get("code_challenge_method")
		if codeChallengeMethod != "" && codeChallengeMethod != "S256" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"only S256 code_challenge_method is supported")
			return
		}

		state := q.Get("state")

		// Auto-approve: generate authorization code and redirect.
		code := codeStore.put(codeChallenge, clientID, redirectURI)

		slog.Info("OAuth authorize: issued authorization code",
			"client_id", clientID,
			"redirect_uri", redirectURI)

		// Build redirect URL with code and state
		redirectURL, err := url.Parse(redirectURI)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"malformed redirect_uri")
			return
		}

		rq := redirectURL.Query()
		rq.Set("code", code)
		if state != "" {
			rq.Set("state", state)
		}
		redirectURL.RawQuery = rq.Encode()

		http.Redirect(w, r, redirectURL.String(), http.StatusFound)
	}
}

// AuthCodeTokenHandler returns an http.HandlerFunc that implements the
// OAuth 2.0 token endpoint supporting the authorization_code grant type
// with PKCE verification. On success it returns the configured MCP auth token.
//
// Panics if authToken is empty.
func AuthCodeTokenHandler(authToken string) http.HandlerFunc {
	if authToken == "" {
		panic("mcphttp.AuthCodeTokenHandler: authToken must not be empty")
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"malformed form body")
			return
		}

		grantType := r.FormValue("grant_type")
		if grantType != "authorization_code" {
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
				"only authorization_code grant type is supported on this endpoint")
			return
		}

		code := r.FormValue("code")
		if code == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing code parameter")
			return
		}

		codeVerifier := r.FormValue("code_verifier")
		if codeVerifier == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"missing code_verifier parameter (PKCE required)")
			return
		}

		// Consume the authorization code (single-use)
		ac, ok := codeStore.consume(code)
		if !ok {
			slog.Warn("OAuth token: invalid or expired authorization code",
				"remote_addr", extractIP(r))
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"authorization code is invalid, expired, or already used")
			return
		}

		// Verify PKCE: SHA256(code_verifier) must match the stored code_challenge
		h := sha256.Sum256([]byte(codeVerifier))
		computedChallenge := base64.RawURLEncoding.EncodeToString(h[:])

		if computedChallenge != ac.codeChallenge {
			slog.Warn("OAuth token: PKCE verification failed",
				"remote_addr", extractIP(r))
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"PKCE code_verifier does not match code_challenge")
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
