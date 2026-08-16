package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/windingriverholdings/openbrain/internal/brain"
)

// fakeWSRequest creates a minimal HTTP request for testing CheckOrigin.
func fakeWSRequest(origin, host string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestParseAllowedOrigins_Empty(t *testing.T) {
	result := parseAllowedOrigins("")
	assert.Nil(t, result)
}

func TestParseAllowedOrigins_Single(t *testing.T) {
	result := parseAllowedOrigins("https://example.com")
	assert.Equal(t, []string{"https://example.com"}, result)
}

func TestParseAllowedOrigins_Multiple(t *testing.T) {
	result := parseAllowedOrigins("https://example.com, https://allowed.example.com, http://localhost:3000")
	assert.Equal(t, []string{"https://example.com", "https://allowed.example.com", "http://localhost:3000"}, result)
}

func TestParseAllowedOrigins_TrimsWhitespace(t *testing.T) {
	result := parseAllowedOrigins("  https://example.com , https://allowed.example.com  ")
	assert.Equal(t, []string{"https://example.com", "https://allowed.example.com"}, result)
}

func TestParseAllowedOrigins_SkipsEmpty(t *testing.T) {
	result := parseAllowedOrigins("https://example.com,,https://allowed.example.com")
	assert.Equal(t, []string{"https://example.com", "https://allowed.example.com"}, result)
}

func TestNewUpgrader_NoAllowedOrigins_RejectsCrossOrigin(t *testing.T) {
	upgrader := newUpgrader("")

	// Simulate a cross-origin request
	result := upgrader.CheckOrigin(fakeWSRequest("http://evil.com", "localhost:10203"))
	assert.False(t, result, "should reject cross-origin when no allowed origins configured")
}

func TestNewUpgrader_NoAllowedOrigins_AllowsSameOrigin(t *testing.T) {
	upgrader := newUpgrader("")

	result := upgrader.CheckOrigin(fakeWSRequest("http://localhost:10203", "localhost:10203"))
	assert.True(t, result, "should allow same-origin request")
}

func TestNewUpgrader_NoAllowedOrigins_AllowsNoOriginHeader(t *testing.T) {
	upgrader := newUpgrader("")

	result := upgrader.CheckOrigin(fakeWSRequest("", "localhost:10203"))
	assert.True(t, result, "should allow request with no Origin header")
}

func TestNewUpgrader_WithAllowedOrigins_AllowsListed(t *testing.T) {
	upgrader := newUpgrader("https://example.com,https://allowed.example.com")

	result := upgrader.CheckOrigin(fakeWSRequest("https://allowed.example.com", "localhost:10203"))
	assert.True(t, result, "should allow listed origin")
}

func TestNewUpgrader_WithAllowedOrigins_RejectsUnlisted(t *testing.T) {
	upgrader := newUpgrader("https://example.com,https://allowed.example.com")

	result := upgrader.CheckOrigin(fakeWSRequest("https://evil.com", "localhost:10203"))
	assert.False(t, result, "should reject unlisted origin")
}

func TestWsResponse_JSONFields(t *testing.T) {
	// The JS client expects fields: content, intent, thought_type
	resp := wsResponse{
		Content:     "test content",
		Intent:      "search",
		ThoughtType: "note",
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	assert.Equal(t, "test content", raw["content"], "JSON must have 'content' field")
	assert.Equal(t, "search", raw["intent"], "JSON must have 'intent' field")
	assert.Equal(t, "note", raw["thought_type"], "JSON must have 'thought_type' field")
	assert.Nil(t, raw["response"], "JSON must NOT have old 'response' field")
}

func TestWsResponse_AmbiguousFields(t *testing.T) {
	data, err := json.Marshal(wsResponse{Content: "choose", Intent: "ambiguous", Ambiguous: true, Query: "tent notes"})
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, true, raw["ambiguous"])
	assert.Equal(t, "tent notes", raw["query"])
}

func TestWsHandler_NoToken_AllowsConnection(t *testing.T) {
	b := brain.New(nil, nil, nil)
	upgrader := newUpgrader("")
	handler := wsHandler(b, upgrader, "")

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "should connect without token when authToken is empty")
	conn.Close()
}

func TestWsHandler_WithToken_RejectsWithout(t *testing.T) {
	b := brain.New(nil, nil, nil)
	upgrader := newUpgrader("")
	handler := wsHandler(b, upgrader, "my-secret-ws-token-for-testing-1234")

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.Error(t, err, "should reject connection without token")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWsHandler_WithToken_AcceptsCorrect(t *testing.T) {
	b := brain.New(nil, nil, nil)
	upgrader := newUpgrader("")
	token := "my-secret-ws-token-for-testing-1234"
	handler := wsHandler(b, upgrader, token)

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "should accept connection with correct token")
	conn.Close()
}

func TestWsHandler_WithToken_RejectsWrong(t *testing.T) {
	b := brain.New(nil, nil, nil)
	upgrader := newUpgrader("")
	handler := wsHandler(b, upgrader, "correct-token-abcdefghijklmnop")

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=wrong-token"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.Error(t, err, "should reject connection with wrong token")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestApiDeleteThought_RejectsNonDelete(t *testing.T) {
	b := brain.New(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/thought-delete/123", nil)
	rr := httptest.NewRecorder()
	apiDeleteThought(b)(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestApiDeleteThought_RejectsMissingID(t *testing.T) {
	b := brain.New(nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/thought-delete/", nil)
	rr := httptest.NewRecorder()
	apiDeleteThought(b)(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestApiDeleteThought_RejectsPathTraversal(t *testing.T) {
	b := brain.New(nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/thought-delete/a/b", nil)
	rr := httptest.NewRecorder()
	apiDeleteThought(b)(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWsHandler_ResponseFormat(t *testing.T) {
	// Create a brain with nil deps — Help intent doesn't use DB or embedder
	b := brain.New(nil, nil, nil)

	upgrader := newUpgrader("")
	handler := wsHandler(b, upgrader, "")

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Send "help" — routed to Help intent, no DB needed
	err = conn.WriteJSON(wsMessage{Message: "help"})
	require.NoError(t, err)

	_, rawMsg, err := conn.ReadMessage()
	require.NoError(t, err)

	var raw map[string]interface{}
	err = json.Unmarshal(rawMsg, &raw)
	require.NoError(t, err)

	// The JS client reads data.content, data.intent, data.thought_type
	assert.NotEmpty(t, raw["content"], "response must include 'content' field")
	assert.NotNil(t, raw["intent"], "response must include 'intent' field")
	assert.NotNil(t, raw["thought_type"], "response must include 'thought_type' field")
	assert.Nil(t, raw["response"], "response must NOT include old 'response' field")
}

type mockBlockingEmbedder struct {
	startBlock chan struct{}
}

func (m *mockBlockingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.startBlock != nil {
		close(m.startBlock)
		m.startBlock = nil // prevent double close
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *mockBlockingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}

func (m *mockBlockingEmbedder) Dimension() int {
	return 384
}

func TestWsHandler_Cancellation(t *testing.T) {
	startChan := make(chan struct{})
	emb := &mockBlockingEmbedder{startBlock: startChan}
	b := brain.New(nil, emb, nil)

	upgrader := newUpgrader("")
	handler := wsHandler(b, upgrader, "")

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Send message to trigger capture (which will block on Embed)
	err = conn.WriteJSON(wsMessage{Message: "Some memory", Intent: "capture"})
	require.NoError(t, err)

	// Wait for Embed to be entered
	<-startChan

	// Send cancel message
	err = conn.WriteJSON(wsMessage{Type: "cancel"})
	require.NoError(t, err)

	// Read reply, we expect to see "canceled" intent
	_, rawMsg, err := conn.ReadMessage()
	require.NoError(t, err)

	var resp wsResponse
	err = json.Unmarshal(rawMsg, &resp)
	require.NoError(t, err)

	assert.Equal(t, "canceled", resp.Intent)
	assert.Equal(t, "Operation stopped.", resp.Content)
}
