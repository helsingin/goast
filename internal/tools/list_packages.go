package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListPackagesArgs are the input arguments for the list-packages tool.
type ListPackagesArgs struct {
	Repo            string `json:"repo,omitempty" jsonschema:"Filter by repository name (e.g. service-api)"`
	ExcludeInternal bool   `json:"exclude_internal,omitempty" jsonschema:"Hide /internal/ packages from the list (default false — internal packages are shown)"`
}

// RegisterListPackages registers the list-packages tool on the server.
func RegisterListPackages(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list-packages",
		Description: "List Go packages across indexed repositories. Internal packages are included by default; pass exclude_internal=true if you want a cleaner list without them. All indexed packages, including internal packages, are also searchable via search-symbols and readable via read-symbol regardless of this flag. Returns package name, import path, repo, doc summary, and symbol count. If you've made code changes recently, call reindex first to pick up new packages.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListPackagesArgs) (*mcp.CallToolResult, any, error) {
		idx := holder.Get()
		pkgs := idx.ListPackages(args.Repo, !args.ExcludeInternal)
		totalPkgs := len(idx.ListPackages("", true))

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d packages (%d total)\n\n", len(pkgs), totalPkgs)

		for _, p := range pkgs {
			fmt.Fprintf(&b, "[PKG] %s (%d symbols)\n", p.Name, p.SymbolCount)
			fmt.Fprintf(&b, "  %s | %s\n", p.ImportPath, p.Repo)
			if p.DocSummary != "" {
				fmt.Fprintf(&b, "  %s\n", p.DocSummary)
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
