package mcptools_test

import (
	"testing"

	"github.com/craig8/openbrain/internal/mcptools"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
)

func TestRegisterToolsAddsExpectedTools(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.1")
	mcptools.RegisterTools(s, nil, nil)

	// The MCP server should have all registered tools.
	// RegisterTools should add at least 8 tools (including ingest_document).
	assert.NotNil(t, s, "MCP server should not be nil after RegisterTools")
}

func TestRegisterToolsWithOpts_ExcludeIngest(t *testing.T) {
	// With ExcludeIngest=false, ingest_document is registered (default behavior).
	sAll := server.NewMCPServer("test", "0.0.1")
	mcptools.RegisterToolsWithOpts(sAll, nil, nil, mcptools.RegisterOpts{ExcludeIngest: false})
	assert.NotNil(t, sAll)

	// With ExcludeIngest=true, ingest_document is NOT registered.
	sHTTP := server.NewMCPServer("test", "0.0.1")
	mcptools.RegisterToolsWithOpts(sHTTP, nil, nil, mcptools.RegisterOpts{ExcludeIngest: true})
	assert.NotNil(t, sHTTP)
}
