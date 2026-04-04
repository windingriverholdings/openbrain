package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
	result := parseAllowedOrigins("https://example.com, https://wr-s.net, http://localhost:3000")
	assert.Equal(t, []string{"https://example.com", "https://wr-s.net", "http://localhost:3000"}, result)
}

func TestParseAllowedOrigins_TrimsWhitespace(t *testing.T) {
	result := parseAllowedOrigins("  https://example.com , https://wr-s.net  ")
	assert.Equal(t, []string{"https://example.com", "https://wr-s.net"}, result)
}

func TestParseAllowedOrigins_SkipsEmpty(t *testing.T) {
	result := parseAllowedOrigins("https://example.com,,https://wr-s.net")
	assert.Equal(t, []string{"https://example.com", "https://wr-s.net"}, result)
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
	upgrader := newUpgrader("https://example.com,https://wr-s.net")

	result := upgrader.CheckOrigin(fakeWSRequest("https://wr-s.net", "localhost:10203"))
	assert.True(t, result, "should allow listed origin")
}

func TestNewUpgrader_WithAllowedOrigins_RejectsUnlisted(t *testing.T) {
	upgrader := newUpgrader("https://example.com,https://wr-s.net")

	result := upgrader.CheckOrigin(fakeWSRequest("https://evil.com", "localhost:10203"))
	assert.False(t, result, "should reject unlisted origin")
}
