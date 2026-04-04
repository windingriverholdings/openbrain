// Package mcphttp provides HTTP-based MCP transport handlers with
// bearer token authentication for the OpenBrain web server.
package mcphttp

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/craig8/openbrain/internal/brain"
	"github.com/craig8/openbrain/internal/embeddings"
	"github.com/craig8/openbrain/internal/mcptools"
	"github.com/mark3labs/mcp-go/server"
)

// BearerAuth wraps an http.Handler with bearer token authentication.
// Requests without a valid "Authorization: Bearer <token>" header
// receive a 401 Unauthorized response.
// Panics if token is empty — an empty token would authenticate every request.
func BearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		panic("mcphttp.BearerAuth: token must not be empty")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// newMCPServer creates a configured MCP server for HTTP transport.
// The ingest_document tool is excluded because it reads the local filesystem
// and must not be exposed over the network.
// Panics if b is nil — the HTTP transport requires a live Brain.
func newMCPServer(name, version string, b *brain.Brain, embedder embeddings.Embedder) *server.MCPServer {
	if b == nil {
		panic("mcphttp.newMCPServer: brain must not be nil for HTTP transport")
	}
	s := server.NewMCPServer(name, version)
	mcptools.RegisterToolsWithOpts(s, b, embedder, mcptools.RegisterOpts{
		ExcludeIngest: true,
	})
	return s
}

// mcpRequestsPerSecond is the per-IP rate limit for authenticated MCP requests.
const mcpRequestsPerSecond = 1.0 // 60 per minute

// mcpBurstSize is the maximum burst for the MCP rate limiter.
const mcpBurstSize = 10

// NewMCPHandler returns an http.Handler for the Streamable HTTP MCP transport,
// wrapped with rate limiting and bearer token authentication. Mount at "/mcp".
func NewMCPHandler(token, name, version string, b *brain.Brain, embedder embeddings.Embedder) http.Handler {
	mcpSrv := newMCPServer(name, version, b, embedder)
	transport := server.NewStreamableHTTPServer(mcpSrv)
	return RateLimit(mcpRequestsPerSecond, mcpBurstSize, BearerAuth(token, transport))
}

// NewSSEHandler returns an http.Handler for the SSE MCP transport,
// wrapped with rate limiting and bearer token authentication. Mount at "/sse/".
// The SSE server registers two internal endpoints:
//   - /sse/sse — the SSE stream endpoint
//   - /sse/message — the message POST endpoint
func NewSSEHandler(token, name, version string, b *brain.Brain, embedder embeddings.Embedder) http.Handler {
	mcpSrv := newMCPServer(name, version, b, embedder)
	sseTransport := server.NewSSEServer(mcpSrv,
		server.WithStaticBasePath("/sse"),
	)
	return RateLimit(mcpRequestsPerSecond, mcpBurstSize, BearerAuth(token, sseTransport))
}
