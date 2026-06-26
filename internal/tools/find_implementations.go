package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// FindImplementationsArgs are the input arguments for the find-implementations tool.
type FindImplementationsArgs struct {
	Package   string `json:"package" jsonschema:"required,Full import path of the package (e.g. github.com/acme/platform/auth)"`
	Name      string `json:"name" jsonschema:"required,Symbol name. For methods use Type.Method format (e.g. KeyManager.GetKey)"`
	Direction string `json:"direction,omitempty" jsonschema:"Query direction: 'implementations' (default) returns concrete types implementing an interface; 'interfaces' returns interfaces satisfied by a type"`
}

// RegisterFindImplementations registers the find-implementations tool on the server.
func RegisterFindImplementations(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find-implementations",
		Description: `Given a KNOWN interface (by exact package + name), returns all concrete types that implement it. Given a KNOWN concrete type, returns all interfaces it satisfies. Requires you to already know the symbol's package and name — use search-symbols first to discover interfaces/types, then use this tool to map their relationships. Uses AST-based method descriptor matching. If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FindImplementationsArgs) (*mcp.CallToolResult, any, error) {
		direction := args.Direction
		if direction == "" {
			direction = "implementations"
		}

		idx := holder.Get()
		var results []index.Symbol
		var header string

		switch direction {
		case "implementations":
			results = idx.FindImplementations(args.Package, args.Name)
			header = fmt.Sprintf("Types implementing %s.%s", args.Package, args.Name)
		case "interfaces":
			results = idx.FindInterfaces(args.Package, args.Name)
			header = fmt.Sprintf("Interfaces satisfied by %s.%s", args.Package, args.Name)
		default:
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Invalid direction %q: must be \"implementations\" or \"interfaces\"", direction)},
				},
				IsError: true,
			}, nil, nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%s: found %d\n\n", header, len(results))

		for _, s := range results {
			kind := strings.ToUpper(string(s.TypeKind))
			fmt.Fprintf(&b, "[%s] %s.%s %s\n", kind, s.PkgName, s.Name, s.Signature)
			fmt.Fprintf(&b, "  %s | %s | %s:%d\n", s.ImportPath, s.Repo, filepath.Base(s.FilePath), s.Line)
			if s.DocSummary != "" {
				fmt.Fprintf(&b, "  %s\n", s.DocSummary)
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
