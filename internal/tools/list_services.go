package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListServicesArgs are the input arguments for the list-services tool.
type ListServicesArgs struct {
	Repo string `json:"repo,omitempty" jsonschema:"Filter by repository name (e.g. service-api)"`
}

// RegisterListServices registers the list-services tool on the server.
func RegisterListServices(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list-services",
		Description: `List all gRPC service interfaces across indexed repositories. Returns every *ServiceServer interface from generated protobuf files with RPC methods, request/response types, and repo location. Use this to map the API surface between systems. Optionally filter by repo. If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListServicesArgs) (*mcp.CallToolResult, any, error) {
		idx := holder.Get()
		results := idx.ListServices(args.Repo)

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d gRPC services", len(results))
		if args.Repo != "" {
			fmt.Fprintf(&b, " in %s", args.Repo)
		}
		fmt.Fprintf(&b, "\n\n")

		for _, s := range results {
			// Strip "ServiceServer" suffix for display name.
			serviceName := strings.TrimSuffix(s.Name, "ServiceServer")
			fmt.Fprintf(&b, "[SERVICE] %s (%s)\n", serviceName, s.Repo)
			fmt.Fprintf(&b, "  %s.%s | %s\n", s.PkgName, s.Name, s.ImportPath)
			if s.DocSummary != "" {
				fmt.Fprintf(&b, "  %s\n", s.DocSummary)
			}
			if len(s.Methods) > 0 {
				fmt.Fprintf(&b, "  Methods:\n")
				for _, m := range s.Methods {
					if strings.HasPrefix(m, "mustEmbed") {
						continue
					}
					fmt.Fprintf(&b, "    %s\n", m)
				}
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
