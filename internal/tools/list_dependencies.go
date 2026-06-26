package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListDependenciesArgs are the input arguments for the list-dependencies tool.
type ListDependenciesArgs struct {
	Repo string `json:"repo,omitempty" jsonschema:"Filter to show only dependencies involving this repo (as importer or importee)"`
}

// RegisterListDependencies registers the list-dependencies tool on the server.
func RegisterListDependencies(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list-dependencies",
		Description: `List cross-repo import dependencies. Shows which repos import packages from which other repos, based on actual Go import statements. Use this to understand the service dependency graph — e.g. which services are clients of which gRPC APIs. Optionally filter by repo to see just that repo's dependencies. If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListDependenciesArgs) (*mcp.CallToolResult, any, error) {
		idx := holder.Get()
		deps := idx.ListDependencies(args.Repo)

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d cross-repo dependencies", len(deps))
		if args.Repo != "" {
			fmt.Fprintf(&b, " involving %s", args.Repo)
		}
		fmt.Fprintf(&b, "\n\n")

		for _, d := range deps {
			fmt.Fprintf(&b, "%s → %s (%d packages)\n", d.FromRepo, d.ToRepo, len(d.ImportPaths))
			for _, p := range d.ImportPaths {
				fmt.Fprintf(&b, "  %s\n", p)
			}
			b.WriteString("\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: b.String()},
			},
		}, nil, nil
	})
}
