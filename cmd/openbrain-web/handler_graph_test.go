package main

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphPageDoesNotAutoGenerateMap(t *testing.T) {
	graphPath := filepath.Join("static", "graph.html")
	data, err := os.ReadFile(graphPath)
	require.NoError(t, err)
	page := string(data)
	composerData, err := os.ReadFile(filepath.Join("static", "composer.js"))
	require.NoError(t, err)
	composer := string(composerData)
	composerCSSData, err := os.ReadFile(filepath.Join("static", "composer.css"))
	require.NoError(t, err)
	composerCSS := string(composerCSSData)

	assert.Contains(t, page, `id="btn-rebuild"`)
	assert.Contains(t, page, "Generate new map")
	assert.Contains(t, page, `id="back-to-chat"`)
	assert.Contains(t, page, "chatURL.search = location.search")
	assert.Contains(t, page, "window.location.assign('/' + location.search)")
	assert.Contains(t, page, `src="/composer.js"`)
	assert.Contains(t, composer, `id="composer"`)
	assert.Contains(t, composer, `id="input"`)
	assert.Contains(t, composer, `id="search-mode"`)
	assert.Contains(t, composer, `id="more-options-btn"`)
	assert.Contains(t, composer, `id="search-options"`)
	assert.Contains(t, composer, `id="search-strategy-wrap"`)
	assert.NotContains(t, page, `id="search-time-wrap"`)
	assert.Contains(t, composer, `data-mode="conversational"`)
	assert.Contains(t, composerCSS, "position: fixed; right: 0; bottom: 0; left: 0")
	assert.NotContains(t, page, `id="search-panel"`)
	assert.NotContains(t, page, `id="search-panel-header"`)
	assert.NotContains(t, page, "runBlockingBuild(")
	assert.NotContains(t, page, "runBackgroundRefresh(")
	assert.Contains(t, page, "await loadData();")
}

// graphFSStub is a minimal fs.FS that serves a fake graph.html so tests do not
// depend on the embedded binary blob or a real brain.json being present.
type graphFSStub struct{}

func (graphFSStub) Open(name string) (fs.File, error) {
	if name == "graph.html" || name == "/graph.html" {
		return &graphFileStub{r: strings.NewReader(graphHTMLBody)}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

const graphHTMLBody = `<!DOCTYPE html><html><body><canvas id="brain-canvas"></canvas></body></html>`

type graphFileStub struct{ r io.Reader }

func (f *graphFileStub) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *graphFileStub) Close() error               { return nil }
func (f *graphFileStub) Stat() (fs.FileInfo, error) { return graphFileInfo{}, nil }

type graphFileInfo struct{}

func (graphFileInfo) Name() string       { return "graph.html" }
func (graphFileInfo) Size() int64        { return int64(len(graphHTMLBody)) }
func (graphFileInfo) Mode() fs.FileMode  { return 0444 }
func (graphFileInfo) ModTime() time.Time { return time.Time{} }
func (graphFileInfo) IsDir() bool        { return false }
func (graphFileInfo) Sys() interface{}   { return nil }

// ── helper ────────────────────────────────────────────────────────────────────

// validToken is a ≥32-char token suitable for WebWSToken validation.
const validToken = "test-token-that-is-long-enough-32chars"

func serveGraph(t *testing.T, token string, target string) *httptest.ResponseRecorder {
	t.Helper()
	h := staticAuth(token, graphHandler(graphFSStub{}))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ── tests ─────────────────────────────────────────────────────────────────────

// No token configured — all requests pass through.
func TestGraphHandler_NoTokenConfigured_Returns200(t *testing.T) {
	rr := serveGraph(t, "", "/graph")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), `id="brain-canvas"`),
		"response body should contain the graph canvas marker")
}

// Token configured, correct token in query → 200.
func TestGraphHandler_CorrectToken_Returns200(t *testing.T) {
	rr := serveGraph(t, validToken, "/graph?token="+validToken)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), `id="brain-canvas"`))
}

// Token configured, no token in query → 401.
func TestGraphHandler_MissingToken_Returns401(t *testing.T) {
	rr := serveGraph(t, validToken, "/graph")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// Token configured, wrong token in query → 401.
func TestGraphHandler_WrongToken_Returns401(t *testing.T) {
	rr := serveGraph(t, validToken, "/graph?token=wrongtoken")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
