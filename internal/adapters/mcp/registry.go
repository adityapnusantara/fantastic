// Package mcp connects the agent to configured external MCP servers and
// exposes their tools as eino tools.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	mcptool "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcptypes "github.com/mark3labs/mcp-go/mcp"

	"github.com/cloudwego/eino/components/tool"

	"github.com/adityapratamanusantara/fantastic/config"
)

// Registry holds the tools of every MCP server that came up, keyed by
// catalog name "<serverName>.<toolName>" so identically named tools from
// different servers stay distinct entries.
type Registry struct {
	catalog   map[string]tool.BaseTool
	serversUp []string
}

// NewRegistry dials each configured server. A server that cannot be
// reached or whose tools fail to load degrades the agent to the remaining
// servers' tools — startup never fails because of one bad server.
func NewRegistry(ctx context.Context, servers config.MCPServers, log *slog.Logger) *Registry {
	r := &Registry{catalog: make(map[string]tool.BaseTool)}
	for name, srv := range servers {
		cli, err := dial(ctx, srv)
		if err != nil {
			log.Warn("mcp server unreachable; continuing without it",
				slog.String("server", name), slog.String("error", err.Error()))
			continue
		}
		tools, err := mcptool.GetTools(ctx, &mcptool.Config{
			Cli:          cli,
			ToolNameList: srv.AllowTools, // empty list fetches all tools
		})
		if err != nil {
			log.Warn("mcp tool discovery failed; continuing without it",
				slog.String("server", name), slog.String("error", err.Error()))
			continue
		}
		added := 0
		for _, tl := range tools {
			tname, err := toolName(ctx, tl)
			if err != nil || tname == "" {
				log.Warn("mcp tool name unavailable; skipping tool",
					slog.String("server", name), slog.String("error", nameError(err)))
				continue
			}
			r.catalog[catalogKey(name, tname)] = tl
			added++
		}
		r.serversUp = append(r.serversUp, name)
		log.Info("mcp server connected",
			slog.String("server", name), slog.Int("tools", added))
	}
	return r
}

// Tools returns every collected eino tool, ordered by catalog name for
// determinism. Callers that do not care about per-server identity can use
// this flat view.
func (r *Registry) Tools() []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(r.catalog))
	for _, name := range r.Names() {
		out = append(out, r.catalog[name])
	}
	return out
}

// Catalog returns a copy of the named tool catalog, keyed
// "<serverName>.<toolName>".
func (r *Registry) Catalog() map[string]tool.BaseTool {
	out := make(map[string]tool.BaseTool, len(r.catalog))
	maps.Copy(out, r.catalog)
	return out
}

// Names lists the catalog keys in sorted order.
func (r *Registry) Names() []string {
	return slices.Sorted(maps.Keys(r.catalog))
}

// Get looks up one tool by its catalog name "<serverName>.<toolName>".
func (r *Registry) Get(name string) (tool.BaseTool, bool) {
	t, ok := r.catalog[name]
	return t, ok
}

// ServersUp lists the names of servers that connected successfully.
func (r *Registry) ServersUp() []string {
	return r.serversUp
}

// catalogKey builds the unique catalog name for one server's tool. The
// server prefix keeps overlapping tool names (two servers both exposing
// "search") as distinct entries.
func catalogKey(server, tool string) string {
	return server + "." + tool
}

// toolName extracts a tool's own declared name through the eino tool
// interface (BaseTool.Info -> schema.ToolInfo.Name).
func toolName(ctx context.Context, t tool.BaseTool) (string, error) {
	info, err := t.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("tool info: %w", err)
	}
	return info.Name, nil
}

// nameError renders a tool-name extraction failure for the skip warning.
func nameError(err error) string {
	if err != nil {
		return err.Error()
	}
	return "empty tool name"
}

// dial builds and initializes a client for one server declaration.
func dial(ctx context.Context, srv config.MCPServerConfig) (*client.Client, error) {
	var cli *client.Client
	var err error

	transportKind := srv.Transport
	if transportKind == "" {
		transportKind = config.DefaultMCPTransport
	}

	switch transportKind {
	case "http":
		opts := make([]transport.StreamableHTTPCOption, 0, 1)
		if len(srv.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(srv.Headers))
		}
		cli, err = client.NewStreamableHttpClient(srv.URL, opts...)
	case "sse":
		opts := make([]transport.ClientOption, 0, 1)
		if len(srv.Headers) > 0 {
			opts = append(opts, client.WithHeaders(srv.Headers))
		}
		cli, err = client.NewSSEMCPClient(srv.URL, opts...)
	default:
		return nil, fmt.Errorf("unknown transport %q (use \"http\" or \"sse\")", srv.Transport)
	}
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	initReq := mcptypes.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcptypes.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcptypes.Implementation{Name: "fantastic", Version: "0.1.0"}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return cli, nil
}
