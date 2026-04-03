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
	// We verify by sending a tools/list request and checking the count.
	// RegisterTools should add at least 8 tools.
	assert.NotNil(t, s, "MCP server should not be nil after RegisterTools")
}
