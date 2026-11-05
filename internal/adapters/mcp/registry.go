// Package mcp connects the agent to configured external MCP servers and
// exposes their tools as eino tools.
package mcp

import (
	"context"
	"fmt"
	"log/slog"

	mcptool "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcptypes "github.com/mark3labs/mcp-go/mcp"

	"github.com/cloudwego/eino/components/tool"

	"github.com/adityapratamanusantara/fantastic/config"
)

// Registry holds the tools of every MCP server that came up.
type Registry struct {
	tools     []tool.BaseTool
	serversUp []string
}

// NewRegistry dials each configured server. A server that cannot be
// reached or whose tools fail to load degrades the agent to the remaining
// servers' tools — startup never fails because of one bad server.
func NewRegistry(ctx context.Context, servers config.MCPServers, log *slog.Logger) *Registry {
	r := &Registry{}
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
		r.tools = append(r.tools, tools...)
		r.serversUp = append(r.serversUp, name)
		log.Info("mcp server connected",
			slog.String("server", name), slog.Int("tools", len(tools)))
	}
	return r
}

// Tools returns the collected eino tools.
func (r *Registry) Tools() []tool.BaseTool {
	return r.tools
}

// ServersUp lists the names of servers that connected successfully.
func (r *Registry) ServersUp() []string {
	return r.serversUp
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
