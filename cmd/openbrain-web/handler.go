package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/config"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/intent"
	"github.com/craig8/openbrain/internal/mcphttp"
)

//go:embed static
var staticFS embed.FS

// newUpgrader creates a WebSocket upgrader with origin validation.
// If allowedOrigins is empty, only same-origin requests are allowed.
func newUpgrader(allowedOrigins string) websocket.Upgrader {
	allowed := parseAllowedOrigins(allowedOrigins)
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // no Origin header — same-origin or non-browser client
			}
			if len(allowed) == 0 {
				// Default: only allow if origin matches the Host header
				return origin == "http://"+r.Host || origin == "https://"+r.Host
			}
			for _, a := range allowed {
				if strings.EqualFold(origin, a) {
					return true
				}
			}
			return false
		},
	}
}

// parseAllowedOrigins splits a comma-separated origin list into a slice.
func parseAllowedOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func serveHTTP(ctx context.Context, cfg *config.Config, b *brain.Brain, embedder embeddings.Embedder) error {
	mux := http.NewServeMux()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	mux.HandleFunc("/api/search", apiSearch(b))
	mux.HandleFunc("/api/capture", apiCapture(b))
	mux.HandleFunc("/api/stats", apiStats(b))
	mux.HandleFunc("/api/review", apiReview(b))

	upgrader := newUpgrader(cfg.WebAllowedOrigins)
	mux.HandleFunc("/ws", wsHandler(b, upgrader, cfg.MCPAuthToken))

	// Mount MCP HTTP transports when enabled
	if cfg.MCPHTTPEnabled && cfg.MCPAuthToken != "" {
		slog.Info("mounting MCP HTTP transport", "endpoints", []string{"/mcp", "/sse/"})
		mux.Handle("/mcp", mcphttp.NewMCPHandler(cfg.MCPAuthToken, cfg.MCPServerName, cfg.MCPServerVersion, b, embedder))
		mux.Handle("/sse/", mcphttp.NewSSEHandler(cfg.MCPAuthToken, cfg.MCPServerName, cfg.MCPServerVersion, b, embedder))

		// Mount OAuth 2.0 endpoints for MCP spec compliance.
		// The MCP spec (2025-03-26) requires authorization code flow with PKCE.
		// Claude.ai's web MCP connector uses fallback paths (/authorize, /token,
		// /register) regardless of what the metadata advertises.
		slog.Info("mounting OAuth 2.0 endpoints",
			"endpoints", []string{
				"/.well-known/oauth-authorization-server",
				"/.well-known/oauth-protected-resource",
				"/authorize",
				"/register",
				"/token",
			})
		mux.HandleFunc("/.well-known/oauth-authorization-server",
			mcphttp.OAuthMetadataHandler(cfg.OAuthIssuer))
		mux.HandleFunc("/.well-known/oauth-protected-resource",
			mcphttp.ProtectedResourceHandler(cfg.OAuthIssuer))

		// Authorization endpoint: auto-approves and redirects with code (PKCE).
		mux.HandleFunc("/authorize", mcphttp.AuthorizeHandler())

		// Dynamic Client Registration (RFC 7591): Claude.ai registers before auth.
		mux.Handle("/register",
			mcphttp.SecureHeaders(
				mcphttp.RateLimit(0.083, 3,
					mcphttp.RegisterHandler())))

		// Token endpoint: supports authorization_code grant (PKCE).
		// Rate-limited aggressively (5 req/min = 0.083 rps, burst 3).
		mux.Handle("/token",
			mcphttp.SecureHeaders(
				mcphttp.RateLimit(0.083, 3,
					mcphttp.AuthCodeTokenHandler(cfg.MCPAuthToken))))

		// Legacy token endpoint for client_credentials grant.
		// Kept for backward compatibility with existing integrations.
		if cfg.OAuthClientID != "" && cfg.OAuthClientSecret != "" {
			mux.Handle("/oauth/token",
				mcphttp.SecureHeaders(
					mcphttp.RateLimit(0.083, 3,
						mcphttp.OAuthTokenHandler(cfg.OAuthClientID, cfg.OAuthClientSecret, cfg.MCPAuthToken))))
		}
	} else if cfg.MCPHTTPEnabled {
		slog.Warn("MCP HTTP transport enabled but OPENBRAIN_MCP_AUTH_TOKEN is empty; transport NOT mounted")
	}

	srv := &http.Server{Addr: cfg.WebAddr(), Handler: mux}

	// Graceful shutdown on context cancellation
	go func() {
		<-ctx.Done()
		slog.Info("shutting down web server")
		srv.Shutdown(context.Background())
	}()

	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func apiSearch(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "missing q parameter", http.StatusBadRequest)
			return
		}

		parsed := intent.ParsedIntent{Intent: intent.Search, Text: query, ThoughtType: "note"}
		result, err := b.Dispatch(r.Context(), parsed, "web")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"result": result})
	}
}

func apiCapture(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			Content     string   `json:"content"`
			ThoughtType string   `json:"thought_type"`
			Tags        []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		if body.ThoughtType == "" {
			body.ThoughtType = intent.InferType(body.Content)
		}

		parsed := intent.ParsedIntent{
			Intent:      intent.Capture,
			Text:        body.Content,
			ThoughtType: body.ThoughtType,
			Tags:        body.Tags,
		}
		result, err := b.Dispatch(r.Context(), parsed, "web")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"result": result})
	}
}

func apiStats(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parsed := intent.ParsedIntent{Intent: intent.Stats, Text: "stats", ThoughtType: "note"}
		result, err := b.Dispatch(r.Context(), parsed, "web")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]string{"result": result})
	}
}

func apiReview(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := 7
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 {
				days = n
			}
		}
		_ = days // TODO: pass configurable days to brain.GetReview

		parsed := intent.ParsedIntent{Intent: intent.Review, Text: "review", ThoughtType: "note"}
		result, err := b.Dispatch(r.Context(), parsed, "web")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]string{"result": result})
	}
}

type wsMessage struct {
	Message string `json:"message"`
}

type wsResponse struct {
	Response string `json:"response"`
}

func wsHandler(b *brain.Brain, upgrader websocket.Upgrader, authToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// When an auth token is configured, require it via query param.
		// WebSocket connections cannot send custom headers from browsers,
		// so the token is passed as ?token=<value>.
		if authToken != "" {
			qToken := r.URL.Query().Get("token")
			if subtle.ConstantTimeCompare([]byte(qToken), []byte(authToken)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("websocket upgrade failed", "error", err)
			return
		}
		defer conn.Close()

		for {
			var msg wsMessage
			if err := conn.ReadJSON(&msg); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					slog.Error("websocket read error", "error", err)
				}
				return
			}

			parsed := intent.Parse(msg.Message)
			result, err := b.Dispatch(r.Context(), parsed, "web")
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			if err := conn.WriteJSON(wsResponse{Response: result}); err != nil {
				slog.Error("websocket write error", "error", err)
				return
			}
		}
	}
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
