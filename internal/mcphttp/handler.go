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
func BearerAuth(token string, next http.Handler) http.Handler {
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

// newMCPServer creates a configured MCP server with all OpenBrain tools registered.
func newMCPServer(b *brain.Brain, embedder embeddings.Embedder) *server.MCPServer {
	s := server.NewMCPServer("openbrain", "0.1.0")
	mcptools.RegisterTools(s, b, embedder)
	return s
}

// NewMCPHandler returns an http.Handler for the Streamable HTTP MCP transport,
// wrapped with bearer token authentication. Mount at "/mcp".
func NewMCPHandler(token string, b *brain.Brain, embedder embeddings.Embedder) http.Handler {
	mcpSrv := newMCPServer(b, embedder)
	transport := server.NewStreamableHTTPServer(mcpSrv)
	return BearerAuth(token, transport)
}

// NewSSEHandler returns an http.Handler for the SSE MCP transport,
// wrapped with bearer token authentication. Mount at "/sse/".
// The SSE server registers two internal endpoints:
//   - /sse/sse — the SSE stream endpoint
//   - /sse/message — the message POST endpoint
func NewSSEHandler(token string, b *brain.Brain, embedder embeddings.Embedder) http.Handler {
	mcpSrv := newMCPServer(b, embedder)
	sseTransport := server.NewSSEServer(mcpSrv,
		server.WithStaticBasePath("/sse"),
	)
	return BearerAuth(token, sseTransport)
}
