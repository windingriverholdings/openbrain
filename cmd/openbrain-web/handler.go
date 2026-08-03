package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/windingriverholdings/openbrain/internal/brain"
	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/embeddings"
	"github.com/windingriverholdings/openbrain/internal/intent"
	"github.com/windingriverholdings/openbrain/internal/mcphttp"
	"github.com/windingriverholdings/openbrain/internal/model"
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

// openBrainAuthMode reports the auth posture of a token-gated surface as
// "open" or "required", for both the startup warning and the /health
// response. It never returns or logs the token value itself.
func openBrainAuthMode(token string) string {
	if token == "" {
		return "open"
	}
	return "required"
}

// healthHandler serves /health. Alongside the plain liveness check, the
// response reports the auth posture of every token-gated surface (web, mcp)
// so an operator can discover an open-mode deployment at runtime (e.g. under
// systemd, where the startup slog.Warn line is easy to miss) instead of only
// at boot. No token value is ever included, only the mode.
func healthHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"auth_mode": map[string]string{
				"web": openBrainAuthMode(cfg.WebWSToken),
				"mcp": openBrainAuthMode(cfg.MCPAuthToken),
			},
		})
	}
}

// webOpenRoutes lists every route staticAuth gates, in the order they are
// registered. Shared between the startup warning and its test so the two
// cannot silently drift apart.
var webOpenRoutes = []string{"/", "/graph", "/brain.json", "/api/rebuild-viz", "/api/rebuild-viz/status", "/api/search", "/api/search/nodes", "/api/thought/", "/api/capture", "/api/stats", "/api/review", "/api/ingest", "/ws"}

// webOpenWriteEndpoints lists the subset of webOpenRoutes that perform a
// write (disk write, brain mutation, or rebuild trigger).
var webOpenWriteEndpoints = []string{"/api/ingest", "/api/capture", "/api/rebuild-viz"}

// warnWebOpenMode logs the loud, structured startup warning for the case
// where cfg.WebWSToken is empty and the whole web surface runs open. Empty is
// a deliberate, operator-selected posture (see Craig's uniform empty=open
// directive), not a misconfiguration, so this names the exposed surface, the
// bind address, and the open write endpoints rather than merely noting the
// condition. Extracted to a standalone function so tests can assert it fires.
func warnWebOpenMode(cfg *config.Config) {
	slog.Warn("web auth token unset; web surface running OPEN with no authentication",
		"bind", cfg.WebAddr(),
		"open_routes", webOpenRoutes,
		"open_write_endpoints", webOpenWriteEndpoints,
		"remediation", "set OPENBRAIN_WEB_WS_TOKEN to require the ?token= query param on every web route")
}

// warnMCPOpenMode logs the loud, structured startup warning for the case
// where cfg.MCPAuthToken is empty and the MCP HTTP transport runs open.
// Extracted to a standalone function so tests can assert it fires.
func warnMCPOpenMode(cfg *config.Config) {
	slog.Warn("MCP HTTP transport running OPEN; OPENBRAIN_MCP_AUTH_TOKEN unset, /mcp and /sse/ accept unauthenticated requests",
		"bind", cfg.WebAddr(),
		"remediation", "set OPENBRAIN_MCP_AUTH_TOKEN to require a bearer token on the MCP transport")
}

// buildMux wires every route onto a fresh http.ServeMux and returns it,
// without binding a listener. Extracted out of serveHTTP as a seam: unit
// tests exercise the real, fully-wired mux via httptest instead of calling
// the bare handler functions in isolation, which is the only way to prove
// the staticAuth/BearerAuth wrapping present at registration time actually
// applies to the routes callers hit in production.
func buildMux(cfg *config.Config, b *brain.Brain, embedder embeddings.Embedder) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static fs: %w", err)
	}
	mux.Handle("/", staticAuth(cfg.WebWSToken, http.FileServer(http.FS(staticSub))))
	mux.Handle("/graph", staticAuth(cfg.WebWSToken, graphHandler(staticSub)))
	mux.Handle("/brain.json", staticAuth(cfg.WebWSToken, brainJSONHandler(staticSub, cfg.VizOutputPath)))
	mux.HandleFunc("/health", healthHandler(cfg))

	vizJob := &vizJobState{}
	mux.Handle("/api/rebuild-viz", staticAuth(cfg.WebWSToken, apiRebuildViz(cfg, vizJob)))
	mux.Handle("/api/rebuild-viz/status", staticAuth(cfg.WebWSToken, apiRebuildVizStatus(cfg, vizJob)))
	mux.Handle("/api/search", staticAuth(cfg.WebWSToken, apiSearch(b)))
	mux.Handle("/api/search/nodes", staticAuth(cfg.WebWSToken, apiSearchNodes(b)))
	mux.Handle("/api/thought/", staticAuth(cfg.WebWSToken, apiGetThought(b)))
	mux.Handle("/api/capture", staticAuth(cfg.WebWSToken, apiCapture(b)))
	mux.Handle("/api/stats", staticAuth(cfg.WebWSToken, apiStats(b)))
	mux.Handle("/api/review", staticAuth(cfg.WebWSToken, apiReview(b)))
	mux.Handle("/api/ingest", staticAuth(cfg.WebWSToken, apiIngest(b, cfg)))

	upgrader := newUpgrader(cfg.WebAllowedOrigins)
	mux.HandleFunc("/ws", wsHandler(b, upgrader, cfg.WebWSToken))
	// Conditional auth posture: when cfg.WebWSToken is set, staticAuth and
	// wsHandler require it via the ?token= query param on every route above;
	// when it is empty, the whole web surface runs open.
	if cfg.WebWSToken == "" {
		warnWebOpenMode(cfg)
	}

	// Mount MCP HTTP transports when enabled. Conditional auth posture: the
	// transport mounts whenever MCP HTTP is enabled; BearerAuth requires the
	// token when set and passes through (open) when empty. The OAuth machinery
	// mounts only when the token is set, because it is the authorization layer
	// for an authenticated transport and has nothing to authorize in open mode.
	if cfg.MCPHTTPEnabled {
		slog.Info("mounting MCP HTTP transport", "endpoints", []string{"/mcp", "/sse/"})
		allowedHosts := cfg.MCPAllowedHostsList()
		mux.Handle("/mcp", mcphttp.NewMCPHandler(cfg.MCPAuthToken, cfg.MCPServerName, cfg.MCPServerVersion, b, embedder, allowedHosts))
		mux.Handle("/sse/", mcphttp.NewSSEHandler(cfg.MCPAuthToken, cfg.MCPServerName, cfg.MCPServerVersion, b, embedder, allowedHosts))

		if cfg.MCPAuthToken == "" {
			warnMCPOpenMode(cfg)
		} else {
			// Mount OAuth 2.0 endpoints for MCP spec compliance.
			// The MCP spec (2025-03-26) requires authorization code flow with PKCE.
			// Claude.ai's web MCP connector uses fallback paths (/authorize, /token,
			// /register) regardless of what the metadata advertises.
			mountOAuthEndpoints(mux, cfg)
		}
	}

	return mux, nil
}

func serveHTTP(ctx context.Context, cfg *config.Config, b *brain.Brain, embedder embeddings.Embedder) error {
	mux, err := buildMux(cfg, b, embedder)
	if err != nil {
		return err
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

// mountOAuthEndpoints mounts the OAuth 2.0 endpoints the MCP transport needs
// for spec compliance. It is called only when cfg.MCPAuthToken is set, so
// cfg.OAuthIssuer is guaranteed present by validateMCPHTTP.
func mountOAuthEndpoints(mux *http.ServeMux, cfg *config.Config) {
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

// apiSearchNodes returns search results as a JSON array with full node metadata
// (id, score, type, tags, summary, content) so the graph page can highlight
// matching nodes by their UUID. Unlike /api/search it does not format results
// as a human-readable string.
func apiSearchNodes(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "missing q parameter", http.StatusBadRequest)
			return
		}

		opts := brain.SearchOpts{Mode: "hybrid"}

		if from := r.URL.Query().Get("from"); from != "" {
			t, err := time.Parse("2006-01-02", from)
			if err != nil {
				http.Error(w, "invalid from date: use YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			opts.CreatedFrom = &t
		}
		if to := r.URL.Query().Get("to"); to != "" {
			t, err := time.Parse("2006-01-02", to)
			if err != nil {
				http.Error(w, "invalid to date: use YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			// Use end-of-day so the to-date is inclusive.
			eod := t.Add(24*time.Hour - time.Nanosecond)
			opts.CreatedTo = &eod
		}

		rows, err := b.Search(r.Context(), query, opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		type nodeResult struct {
			ID        string   `json:"id"`
			Score     float64  `json:"score"`
			Type      string   `json:"type"`
			Tags      []string `json:"tags"`
			Summary   string   `json:"summary"`
			Content   string   `json:"content"`
			CreatedAt string   `json:"created_at"`
		}

		results := make([]nodeResult, 0, len(rows))
		for _, row := range rows {
			summary := ""
			if row.Summary != nil {
				summary = *row.Summary
			}
			score := 0.0
			if row.Score != nil {
				score = *row.Score
			}
			// Truncate content for the panel preview; full text is in the tooltip.
			content := row.Content
			if len(content) > 200 {
				content = content[:200] + "…"
			}
			tags := row.Tags
			if tags == nil {
				tags = []string{}
			}
			results = append(results, nodeResult{
				ID:        row.ID,
				Score:     score,
				Type:      row.ThoughtType,
				Tags:      tags,
				Summary:   summary,
				Content:   content,
				CreatedAt: row.CreatedAt.Format("2006-01-02"),
			})
		}

		jsonResponse(w, results)
	}
}

// apiGetThought returns a single thought by UUID for the detail panel.
// Route: GET /api/thought/{uuid}
func apiGetThought(b *brain.Brain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/thought/")
		if id == "" {
			http.Error(w, "missing thought id", http.StatusBadRequest)
			return
		}

		thought, err := b.GetThought(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if thought == nil {
			http.Error(w, "thought not found", http.StatusNotFound)
			return
		}

		type thoughtDetail struct {
			ID        string   `json:"id"`
			Type      string   `json:"type"`
			Tags      []string `json:"tags"`
			Source    string   `json:"source"`
			Summary   string   `json:"summary"`
			Content   string   `json:"content"`
			CreatedAt string   `json:"created_at"`
		}

		summary := ""
		if thought.Summary != nil {
			summary = *thought.Summary
		}
		tags := thought.Tags
		if tags == nil {
			tags = []string{}
		}
		jsonResponse(w, thoughtDetail{
			ID:        thought.ID,
			Type:      thought.ThoughtType,
			Tags:      tags,
			Source:    thought.Source,
			Summary:   summary,
			Content:   thought.Content,
			CreatedAt: thought.CreatedAt.Format("2006-01-02 15:04"),
		})
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
	Intent  string `json:"intent,omitempty"`
}

// wsSearchResult is one structured search hit sent to the chat UI. The field
// names and JSON tags deliberately mirror apiSearchNodes' nodeResult so the
// websocket and REST search payloads stay symmetrical and the frontend can
// treat a hit from either source identically.
//
// Unlike apiSearchNodes, Content is NOT truncated: the chat UI clamps long
// bodies visually and expands them in place, so it needs the full text.
type wsSearchResult struct {
	ID        string   `json:"id"`
	Score     float64  `json:"score"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags"`
	Summary   string   `json:"summary"`
	Content   string   `json:"content"`
	CreatedAt string   `json:"created_at"`
}

// wsResponse is the websocket reply envelope.
//
// Results is populated for search intents only and is omitted entirely for
// every other intent, so non-search replies serialize exactly as they always
// have. Content remains populated for search too: it is the plain-text
// rendering older clients (and any non-card fallback path) rely on.
type wsResponse struct {
	Content     string           `json:"content"`
	Intent      string           `json:"intent"`
	ThoughtType string           `json:"thought_type"`
	Results     []wsSearchResult `json:"results,omitempty"`
	Ambiguous   bool             `json:"ambiguous,omitempty"`
	Query       string           `json:"query,omitempty"`
}

// toWSSearchResults converts search rows into the structured websocket payload.
// Tags is normalized to a non-nil slice so the JSON is always an array rather
// than null, matching apiSearchNodes.
func toWSSearchResults(rows []model.ThoughtRow) []wsSearchResult {
	results := make([]wsSearchResult, 0, len(rows))
	for _, row := range rows {
		summary := ""
		if row.Summary != nil {
			summary = *row.Summary
		}
		score := 0.0
		if row.Score != nil {
			score = *row.Score
		}
		tags := row.Tags
		if tags == nil {
			tags = []string{}
		}
		results = append(results, wsSearchResult{
			ID:        row.ID,
			Score:     score,
			Type:      row.ThoughtType,
			Tags:      tags,
			Summary:   summary,
			Content:   row.Content,
			CreatedAt: row.CreatedAt.Format("2006-01-02"),
		})
	}
	return results
}

// staticAuth wraps a handler so that, when authToken is non-empty, requests must
// carry the token via the ?token= query parameter (the same mechanism used by
// wsHandler).  When authToken is empty the handler is passed through unchanged.
func staticAuth(authToken string, next http.Handler) http.Handler {
	if authToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qToken := r.URL.Query().Get("token")
		if subtle.ConstantTimeCompare([]byte(qToken), []byte(authToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// graphHandler serves graph.html at the /graph route without requiring the .html
// suffix in the URL.
func graphHandler(staticSub fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticSub, "graph.html")
	})
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
			if msg.Intent == string(intent.Search) || msg.Intent == string(intent.Capture) {
				parsed.Intent = intent.Intent(msg.Intent)
				parsed.Text = msg.Message
				if parsed.Intent == intent.Capture {
					parsed.ThoughtType = intent.InferType(msg.Message)
				}
			}

			resp := wsResponse{
				Intent:      string(parsed.Intent),
				ThoughtType: parsed.ThoughtType,
			}
			if parsed.Intent == intent.Ambiguous {
				resp.Ambiguous = true
				resp.Query = msg.Message
				resp.Content = "I’m not sure whether you want to search for this or save it as a note."
				if err := conn.WriteJSON(resp); err != nil {
					slog.Error("websocket write error", "error", err)
					return
				}
				continue
			}

			// Search is handled here rather than through Dispatch so the
			// structured rows survive to the client. Dispatch would flatten
			// them to a string, and re-running the search afterwards to
			// recover them would embed the query twice (two Ollama round
			// trips). The hybrid search itself is unchanged: same
			// SearchOpts{Mode: "hybrid"} Dispatch uses, and the plain-text
			// content field is rendered from these same rows by the shared
			// formatter, so it is byte-identical to the old reply.
			if parsed.Intent == intent.Search {
				rows, err := b.Search(r.Context(), parsed.Text, brain.SearchOpts{Mode: "hybrid"})
				if err != nil {
					resp.Content = fmt.Sprintf("Error: %v", err)
				} else {
					resp.Content = brain.FormatSearchResults(rows)
					resp.Results = toWSSearchResults(rows)
				}
			} else {
				result, err := b.Dispatch(r.Context(), parsed, "web")
				if err != nil {
					result = fmt.Sprintf("Error: %v", err)
				}
				resp.Content = result
			}

			if err := conn.WriteJSON(resp); err != nil {
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

// brainJSONHandler serves brain.json from disk when vizOutputPath is set,
// falling back to the embedded copy otherwise. Serving from disk lets the
// rebuild endpoint update the file without restarting the server.
func brainJSONHandler(staticSub fs.FS, vizOutputPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if vizOutputPath != "" {
			data, err := os.ReadFile(vizOutputPath)
			if err == nil {
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			}
			slog.Warn("brain.json: disk read failed, falling back to embedded", "path", vizOutputPath, "error", err)
		}
		http.ServeFileFS(w, r, staticSub, "brain.json")
	})
}

// vizDegradedMarker is the machine-readable line build-brain-viz.py prints to
// stdout when every cluster fell back to heuristic labels (Ollama was
// unreachable). The script still exits 0 in that case: the map is valid,
// just labeled without LLM assistance, so apiRebuildViz scans the captured
// output for this marker rather than relying on the exit code to know
// whether the build is degraded.
const vizDegradedMarker = "BRAIN_VIZ_DEGRADED=true"

// vizJobState tracks the single in-flight (or most recently finished) brain
// map rebuild. There is at most one build-brain-viz.py process running at a
// time: running is both the concurrency guard apiRebuildViz enforces before
// spawning a goroutine, and the field apiRebuildVizStatus reads to report
// state/degraded/error. All access goes through the methods below, which
// hold mu for the duration of the read or write.
type vizJobState struct {
	mu       sync.Mutex
	running  bool
	hasRun   bool // a build has completed at least once; distinguishes "idle" from "done"/"error"
	lastErr  error
	degraded bool
}

// tryStart flips running to true and reports success, or reports failure
// without changing state if a rebuild is already in flight. This is the
// concurrency guard: apiRebuildViz calls it before spawning the goroutine,
// so of any number of overlapping POSTs, exactly one wins the race and the
// rest observe running already true and return 409.
//
// It also clears lastErr/degraded from the PRIOR run. Without this, a fresh
// run's mid-flight /status response would still report the previous run's
// degraded/error values: apiRebuildVizStatus only recomputes errMsg for the
// hasRun-and-lastErr-non-nil branch, but passes degraded straight through
// from the snapshot regardless of state, so a stale "degraded":true (or a
// stale error) would sit alongside "state":"running" until THIS run's own
// job.finish overwrites it.
func (j *vizJobState) tryStart() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running {
		return false
	}
	j.running = true
	j.lastErr = nil
	j.degraded = false
	return true
}

// finish records the outcome of a completed rebuild and clears running.
func (j *vizJobState) finish(err error, degraded bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.running = false
	j.hasRun = true
	j.lastErr = err
	j.degraded = degraded
}

// snapshot copies the job state under lock so callers can read it without
// holding the mutex across a JSON encode.
func (j *vizJobState) snapshot() (running, hasRun, degraded bool, lastErr error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running, j.hasRun, j.degraded, j.lastErr
}

// vizProgressPath derives the progress sidecar path from vizOutputPath, so
// no separate config var is needed for it. build-brain-viz.py (phase 2)
// writes this file atomically (temp + os.replace) at phase boundaries and
// after each cluster is labeled; apiRebuildVizStatus reads it back.
func vizProgressPath(vizOutputPath string) string {
	return vizOutputPath + ".progress.json"
}

// vizProgress is the shape of the progress sidecar file. Field names and
// values match the schema build-brain-viz.py (phase 2) writes: pct 0-100,
// phase one of loading/projecting/clustering/edges/labeling/writing/done,
// clusters_done/clusters_total tracking the per-cluster labeling loop.
type vizProgress struct {
	Pct           int    `json:"pct"`
	Phase         string `json:"phase"`
	ClustersDone  int    `json:"clusters_done"`
	ClustersTotal int    `json:"clusters_total"`
}

// readVizProgress reads the progress sidecar file. A missing file (no build
// has started, or the sidecar has not been created yet), or one caught
// mid-write, is not an error: it means "no progress yet", so the zero-valued
// vizProgress is returned. Malformed JSON is treated the same way: the
// writer's os.replace is atomic, but a defensive reader should not turn a
// transient race into a 500.
func readVizProgress(path string) vizProgress {
	data, err := os.ReadFile(path)
	if err != nil {
		return vizProgress{}
	}
	var p vizProgress
	if err := json.Unmarshal(data, &p); err != nil {
		return vizProgress{}
	}
	return p
}

// resetVizProgress removes the progress sidecar file (if present) so a
// freshly started rebuild does not momentarily report the PRIOR run's
// pct:100/phase:"done" progress while the new process is still starting up
// and has not yet written its own first progress update. readVizProgress
// already tolerates a missing sidecar as zero-value progress (the "no
// rebuild has started yet" case), so removal is equivalent to, and simpler
// than, writing a fresh "starting" record: there is no second schema to keep
// in sync with build-brain-viz.py's writer. A missing file (nothing to
// remove: this is the very first rebuild) is not an error and is not logged.
func resetVizProgress(vizOutputPath string) {
	if err := os.Remove(vizProgressPath(vizOutputPath)); err != nil && !os.IsNotExist(err) {
		slog.Warn("rebuild-viz: failed to reset progress sidecar for new run", "error", err)
	}
}

// vizOutputTailMaxLen bounds how much of a failed rebuild's captured
// stdout/stderr is echoed into the job-state error (and therefore into
// /api/rebuild-viz/status's "error" field, and the UI). Uncapped, a runaway
// script could turn the status endpoint into an unbounded log dump; capped,
// enough of the tail survives to show WHY it failed (missing deps, DB down,
// Ollama down) without that risk.
const vizOutputTailMaxLen = 800

// libpqCredentialRe matches libpq keyword/value connection-string credential
// tokens (password=... or pwd=...), case-insensitive. build-brain-viz.py
// builds its Postgres conninfo in libpq keyword/value form (e.g. "host=...
// user=... password=<value> sslmode=disable"), never as a URL, so the
// scheme://user:pass@host redaction below does not see it.
//
// libpq values are whitespace-delimited unless single-quoted. This pattern
// matches either a single-quoted value (which may itself contain spaces) or
// a bare non-whitespace token. A password containing an UNQUOTED space (the
// shape that actually leaks: build-brain-viz.py does not quote the password
// when it builds the conninfo string, so a spaced passphrase both breaks the
// conninfo parse AND leaks) only has its first whitespace-delimited token
// redacted; anything after the first space is not distinguishable from
// surrounding connection-string keywords and is left alone. This mirrors the
// URL-DSN redaction below: both scrub the known credential shape, not every
// possible substring that could theoretically be a password fragment.
var libpqCredentialRe = regexp.MustCompile(`(?i)\b(password|pwd)=('[^']*'|\S+)`)

// redactLikelyCredentialsInLine scans a single line of subprocess output for
// credential-bearing patterns and redacts them:
//   - a scheme://user:pass@host pattern (a DB DSN, or a URL with embedded
//     credentials), mirroring internal/db.redactDSN
//   - a libpq keyword/value password=... or pwd=... token (see
//     libpqCredentialRe)
//
// Ordinary Python tracebacks and print() output rarely match either pattern,
// so this is a no-op for the overwhelming majority of lines.
func redactLikelyCredentialsInLine(line string) string {
	line = redactURLCredentials(line)
	return libpqCredentialRe.ReplaceAllString(line, "${1}=[REDACTED]")
}

// redactURLCredentials redacts the credential portion of a scheme://user:pass@host
// pattern (a DB DSN, or a URL with embedded credentials), mirroring
// internal/db.redactDSN.
func redactURLCredentials(line string) string {
	schemeIdx := strings.Index(line, "://")
	if schemeIdx == -1 {
		return line
	}
	rest := line[schemeIdx+3:]
	atIdx := strings.Index(rest, "@")
	if atIdx == -1 {
		return line
	}
	return line[:schemeIdx+3] + "***@" + rest[atIdx+1:]
}

// boundedOutputTail trims a failed rebuild's captured output to at most
// vizOutputTailMaxLen bytes, keeping the END of the output (the traceback or
// final error line is almost always last) rather than the start, and
// redacting any line that looks like a DSN or URL with embedded credentials.
func boundedOutputTail(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, line := range lines {
		lines[i] = redactLikelyCredentialsInLine(line)
	}
	tail := strings.Join(lines, "\n")
	if len(tail) <= vizOutputTailMaxLen {
		return tail
	}
	return "...(truncated)...\n" + tail[len(tail)-vizOutputTailMaxLen:]
}

// classifyVizRebuildFailure turns a failed rebuild's process error, the
// context's own error (nil unless the timeout fired), and the captured
// output into the error surfaced via job.finish (and therefore
// /api/rebuild-viz/status's "error" field).
//
// A context deadline exceeded is reported distinctly ("rebuild timed out
// after ...") instead of the opaque "signal: killed" exec.CommandContext
// leaves behind when it kills the process on timeout: the two failure modes
// need different operator responses (a hung/unreachable Ollama vs. an
// ordinary script error), and "signal: killed" alone gives no hint which one
// happened.
//
// Any other failure is wrapped with a bounded tail of the captured output
// (see boundedOutputTail), so an operator can see WHY it failed without
// reading the server log.
func classifyVizRebuildFailure(cmdErr error, ctxErr error, out []byte, timeout time.Duration) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("rebuild timed out after %s; the generator may be hung or Ollama unreachable", timeout)
	}
	tail := boundedOutputTail(out)
	if tail == "" {
		return fmt.Errorf("rebuild failed: %w", cmdErr)
	}
	return fmt.Errorf("rebuild failed: %w: %s", cmdErr, tail)
}

// runVizRebuild runs build-brain-viz.py to completion and records the
// outcome on job. It owns the process end-to-end and is deliberately NOT
// tied to any request context: the goroutine outlives the HTTP request that
// triggered it (the handler returns 202 immediately), and a client
// disconnecting must not kill a rebuild another poller is waiting on.
// cfg.VizRebuildTimeoutOrDefault() is a safety bound on Ollama being slow or
// unreachable, not the expected duration (see plan.md).
func runVizRebuild(cfg *config.Config, job *vizJobState) {
	timeout := cfg.VizRebuildTimeoutOrDefault()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.VizPythonInterpreter(), cfg.VizScriptPath, "--output", cfg.VizOutputPath, "--progress-file", vizProgressPath(cfg.VizOutputPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("rebuild-viz failed", "error", err, "output", string(out))
		job.finish(classifyVizRebuildFailure(err, ctx.Err(), out, timeout), false)
		return
	}

	// A successful exit does not mean a fully-labeled map: the script exits 0
	// even when every cluster fell back to heuristic labels (Ollama
	// unreachable). Surface that degradation instead of silently discarding
	// it, without failing a rebuild that otherwise succeeded.
	degraded := strings.Contains(string(out), vizDegradedMarker)
	if degraded {
		slog.Warn("rebuild-viz succeeded with degraded cluster labels (heuristic only, LLM unreachable)",
			"output_path", cfg.VizOutputPath, "output", string(out))
	} else {
		slog.Info("rebuild-viz succeeded", "output_path", cfg.VizOutputPath)
	}
	job.finish(nil, degraded)
}

// apiRebuildViz triggers a brain.json rebuild via build-brain-viz.py.
// Route: POST /api/rebuild-viz
// Auth: handled by staticAuth at registration time.
// Requires OPENBRAIN_VIZ_SCRIPT_PATH and OPENBRAIN_VIZ_OUTPUT_PATH to be set.
//
// Async: the handler never blocks on the script. It returns 202 and starts
// the rebuild in a goroutine, or 409 if a rebuild is already running (the
// concurrency guard: job.tryStart ensures only one build-brain-viz.py process
// runs at a time regardless of how many callers POST concurrently). Callers
// poll GET /api/rebuild-viz/status for progress and outcome.
func apiRebuildViz(cfg *config.Config, job *vizJobState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if cfg.VizScriptPath == "" || cfg.VizOutputPath == "" {
			http.Error(w, "OPENBRAIN_VIZ_SCRIPT_PATH and OPENBRAIN_VIZ_OUTPUT_PATH must be set to enable rebuild", http.StatusServiceUnavailable)
			return
		}

		if !job.tryStart() {
			http.Error(w, "rebuild already in progress", http.StatusConflict)
			return
		}

		// Reset the progress sidecar synchronously, before returning 202, so a
		// client that immediately polls /status after triggering never
		// observes the PRIOR run's stale pct/phase (a race the goroutine
		// below cannot close: it may not reach its own first progress write
		// for some time after the process starts).
		resetVizProgress(cfg.VizOutputPath)

		go runVizRebuild(cfg, job)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "started"})
	}
}

// vizIsStale reports whether brain.json is older than ttl, given elapsed
// (time.Since(mtime)). A zero or negative ttl disables the staleness check
// entirely, regardless of elapsed. The comparison is strictly greater-than:
// a file exactly ttl old is NOT stale. Extracted to a pure function because
// the exact-equality boundary (elapsed == ttl) cannot be reproduced
// deterministically through a real file mtime and time.Since call: by the
// time the check runs, elapsed has always advanced past whatever offset a
// test set up.
func vizIsStale(ttl, elapsed time.Duration) bool {
	return ttl > 0 && elapsed > ttl
}

// apiRebuildVizStatus reports the state of the brain-map rebuild job, merging
// three sources: the in-memory job state (state/degraded/error), the
// progress sidecar file build-brain-viz.py writes while it runs
// (pct/phase/clusters_done/clusters_total), and brain.json's own presence
// and mtime (exists/stale, per OPENBRAIN_VIZ_TTL). The frontend (phase 3)
// polls this endpoint to drive a determinate progress bar and to decide
// whether a rebuild needs triggering at all.
// Route: GET /api/rebuild-viz/status
// Auth: handled by staticAuth at registration time.
func apiRebuildVizStatus(cfg *config.Config, job *vizJobState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		running, hasRun, degraded, lastErr := job.snapshot()

		state := "idle"
		errMsg := ""
		switch {
		case running:
			state = "running"
		case hasRun && lastErr != nil:
			state = "error"
			errMsg = lastErr.Error()
		case hasRun:
			state = "done"
		}

		progress := readVizProgress(vizProgressPath(cfg.VizOutputPath))

		exists := false
		stale := false
		if info, statErr := os.Stat(cfg.VizOutputPath); statErr == nil {
			exists = true
			stale = vizIsStale(cfg.VizTTL, time.Since(info.ModTime()))
		}

		jsonResponse(w, map[string]any{
			"state":          state,
			"pct":            progress.Pct,
			"phase":          progress.Phase,
			"clusters_done":  progress.ClustersDone,
			"clusters_total": progress.ClustersTotal,
			"exists":         exists,
			"stale":          stale,
			"degraded":       degraded,
			"error":          errMsg,
		})
	}
}
