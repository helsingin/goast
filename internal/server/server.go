package server

import (
	"github.com/helsingin/goast/internal/index"
	"github.com/helsingin/goast/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// New creates a new MCP server with all tools registered.
func New(holder *index.IndexHolder) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "goast",
		Version: Version,
	}, nil)

	tools.RegisterListPackages(server, holder)
	tools.RegisterSearchSymbols(server, holder)
	tools.RegisterReadSymbol(server, holder)
	tools.RegisterFindImplementations(server, holder)
	tools.RegisterFindReferences(server, holder)
	tools.RegisterListServices(server, holder)
	tools.RegisterListDependencies(server, holder)
	tools.RegisterSearchConfig(server, holder)
	tools.RegisterCrossReference(server, holder)
	tools.RegisterReindex(server, holder)

	return server
}
