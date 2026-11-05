// Command fantastic is the personal AI agent backend.
//
//	@title			fantastic API
//	@version		0.1.0
//	@description	Personal AI agent backend: eino ADK chat over SSE, MCP tools, Postgres memory, Casdoor auth.
//	@BasePath		/api/v1
//	@SecurityDefinition	bearerAuth
//	@name			Authorization
//	@in				header
//	@description	"Bearers {access token}" (Casdoor-issued JWT).
package main

import (
	"os"

	"github.com/adityapratamanusantara/fantastic/internal/cmd"
)

func main() {
	if err := cmd.Run(os.Args); err != nil {
		os.Exit(1)
	}
}
