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
	return newEchoMCPServer(t, "", "echo_a", "echo_b")
}

// newEchoMCPServer spins up a real MCP server over streamable HTTP exposing
// one echo tool per given name. Echoes are tagged with label (when set) so
// tests can tell identically named tools on different servers apart.
func newEchoMCPServer(t *testing.T, label string, toolNames ...string) string {
	t.Helper()
	s := mcpserver.NewMCPServer("test-mcp", "1.0.0",
		mcpserver.WithToolCapabilities(false))

	echo := func(name string) (mcptypes.Tool, mcpserver.ToolHandlerFunc) {
		tool := mcptypes.NewTool(name, mcptypes.WithDescription("echo tool "+name))
		echoed := name
		if label != "" {
			echoed = label + "/" + name
		}
		handler := func(ctx context.Context, req mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			raw, _ := json.Marshal(map[string]string{"echoed": echoed})
			return mcptypes.NewToolResultText(string(raw)), nil
		}
		return tool, handler
	}
	for _, name := range toolNames {
		tl, h := echo(name)
		s.AddTool(tl, h)
	}

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

// invoke runs an eino tool and returns the echoed payload carried in its
// first text content block (InvokableRun marshals the whole CallToolResult).
func invoke(t *testing.T, tl tool.BaseTool) string {
	t.Helper()
	it, ok := tl.(tool.InvokableTool)
	require.True(t, ok, "mcp tool must be invokable")
	out, err := it.InvokableRun(context.Background(), "{}")
	require.NoError(t, err)
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.NotEmpty(t, result.Content, "tool result carries a text block")
	var payload struct {
		Echoed string `json:"echoed"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &payload))
	return payload.Echoed
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

	catalog := reg.Catalog()
	assert.Equal(t, []string{"fake.echo_a"}, reg.Names(), "catalog keys are <server>.<tool>")
	got, ok := reg.Get("fake.echo_a")
	require.True(t, ok, "Get finds the allowlisted tool by catalog name")
	assert.Contains(t, names(t, []tool.BaseTool{got}), "echo_a")
	_, ok = reg.Get("fake.echo_b")
	assert.False(t, ok, "unlisted tool is absent from the catalog")
	assert.Len(t, catalog, 1)
}

func TestRegistryWithoutAllowlistExposesAll(t *testing.T) {
	ctx := context.Background()
	fakeURL := newFakeMCPServer(t)

	reg := mcp.NewRegistry(ctx, config.MCPServers{
		"fake": {URL: fakeURL},
	}, slog.Default())

	assert.ElementsMatch(t, []string{"echo_a", "echo_b"}, names(t, reg.Tools()))
	assert.ElementsMatch(t, []string{"fake.echo_a", "fake.echo_b"}, reg.Names())
	require.Len(t, reg.Tools(), 2, "Tools still returns every catalog entry")
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

	catalog := reg.Catalog()
	assert.NotContains(t, catalog, "dead.echo_a", "dead server contributes no catalog entries")
	assert.Contains(t, catalog, "fake.echo_a")
	_, ok := reg.Get("dead.echo_a")
	assert.False(t, ok, "Get misses the dead server's tools without erroring")
}

func TestRegistryCatalogDisambiguatesOverlappingToolNames(t *testing.T) {
	ctx := context.Background()
	serverA := newEchoMCPServer(t, "a", "search")
	serverB := newEchoMCPServer(t, "b", "search")

	reg := mcp.NewRegistry(ctx, config.MCPServers{
		"a": {URL: serverA},
		"b": {URL: serverB},
	}, slog.Default())

	assert.ElementsMatch(t, []string{"a.search", "b.search"}, reg.Names(),
		"overlapping tool names stay distinct catalog entries")

	gotA, okA := reg.Get("a.search")
	gotB, okB := reg.Get("b.search")
	require.True(t, okA)
	require.True(t, okB)
	assert.Equal(t, "a/search", invoke(t, gotA), "a.search routes to server a's tool")
	assert.Equal(t, "b/search", invoke(t, gotB), "b.search routes to server b's tool")

	_, okC := reg.Get("search")
	assert.False(t, okC, "the bare tool name alone is not a catalog key")

	require.Len(t, reg.Tools(), 2, "Tools still returns both prefixed tools")
	assert.ElementsMatch(t, []string{"search", "search"}, names(t, reg.Tools()))
}
