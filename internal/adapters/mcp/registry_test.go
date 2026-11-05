package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	mcptypes "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/mcp"
)

// newFakeMCPServer spins up a real MCP server over streamable HTTP that
// exposes two tools: echo_a and echo_b.
func newFakeMCPServer(t *testing.T) string {
	t.Helper()
	s := mcpserver.NewMCPServer("test-mcp", "1.0.0",
		mcpserver.WithToolCapabilities(false))

	echo := func(name string) (mcptypes.Tool, mcpserver.ToolHandlerFunc) {
		tool := mcptypes.NewTool(name, mcptypes.WithDescription("echo tool "+name))
		handler := func(ctx context.Context, req mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			raw, _ := json.Marshal(map[string]string{"echoed": name})
			return mcptypes.NewToolResultText(string(raw)), nil
		}
		return tool, handler
	}
	ta, ha := echo("echo_a")
	tb, hb := echo("echo_b")
	s.AddTool(ta, ha)
	s.AddTool(tb, hb)

	srv := mcpserver.NewStreamableHTTPServer(s)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

func names(t *testing.T, tools []tool.BaseTool) []string {
	t.Helper()
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		require.NoError(t, err)
		out = append(out, info.Name)
	}
	return out
}

func TestRegistryConnectsWithAllowlist(t *testing.T) {
	ctx := context.Background()
	fakeURL := newFakeMCPServer(t)

	reg := mcp.NewRegistry(ctx, config.MCPServers{
		"fake": {URL: fakeURL, AllowTools: []string{"echo_a"}},
	}, slog.Default())

	assert.Equal(t, []string{"fake"}, reg.ServersUp())
	require.Len(t, reg.Tools(), 1)
	assert.Contains(t, names(t, reg.Tools()), "echo_a")
	assert.NotContains(t, names(t, reg.Tools()), "echo_b", "unlisted tool must not be exposed")
}

func TestRegistryWithoutAllowlistExposesAll(t *testing.T) {
	ctx := context.Background()
	fakeURL := newFakeMCPServer(t)

	reg := mcp.NewRegistry(ctx, config.MCPServers{
		"fake": {URL: fakeURL},
	}, slog.Default())

	assert.ElementsMatch(t, []string{"echo_a", "echo_b"}, names(t, reg.Tools()))
}

func TestRegistryDegradesWhenOneServerDown(t *testing.T) {
	ctx := context.Background()
	fakeURL := newFakeMCPServer(t)

	reg := mcp.NewRegistry(ctx, config.MCPServers{
		"dead": {URL: "http://127.0.0.1:1/mcp"}, // connection refused
		"fake": {URL: fakeURL},
	}, slog.Default())

	assert.Equal(t, []string{"fake"}, reg.ServersUp(), "dead server is skipped")
	assert.NotEmpty(t, reg.Tools(), "reachable server's tools are still exposed")
}
