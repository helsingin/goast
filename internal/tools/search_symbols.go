package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchSymbolsArgs are the input arguments for the search-symbols tool.
type SearchSymbolsArgs struct {
	Query            string `json:"query,omitempty" jsonschema:"Search query: substring match on symbol name, doc summary, and import path. Prefix with 'exact:' for exact name match. Optional — omit to list all symbols matching other filters."`
	Kind             string `json:"kind,omitempty" jsonschema:"Filter by symbol kind: func, method, type, interface, struct, const, var"`
	Repo             string `json:"repo,omitempty" jsonschema:"Filter by repository name (e.g. service-api)"`
	Package          string `json:"package,omitempty" jsonschema:"Filter by import path or prefix (e.g. github.com/acme/platform/auth)"`
	ExportedOnly     bool   `json:"exported_only,omitempty" jsonschema:"Only show exported symbols (default true)"`
	Receiver         string `json:"receiver,omitempty" jsonschema:"Filter methods by receiver type name"`
	IncludeGenerated bool   `json:"include_generated,omitempty" jsonschema:"Include symbols from generated .pb.go files (default false)"`
	Limit            int    `json:"limit,omitempty" jsonschema:"Max results to return (default 100)"`
}

// RegisterSearchSymbols registers the search-symbols tool on the server.
func RegisterSearchSymbols(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search-symbols",
		Description: `Search for Go symbols across indexed repositories. Query matches symbol names, doc comments, and import paths, so searching a domain term can find packages whose import path contains that term. Query is optional: omit it and use kind/repo/package filters to list all matching symbols, such as kind=interface repo=service-api. Results show repo coverage, for example "matched 3 of 16 repos"; if coverage is low, try related or synonym terms. For broad discovery questions, make multiple calls with related terms to get comprehensive cross-repo coverage. If you've made code changes recently, call reindex first to pick up new or renamed symbols.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SearchSymbolsArgs) (*mcp.CallToolResult, any, error) {
		// Default exported_only to true when not explicitly set.
		// Since Go zero-value for bool is false, we use a convention:
		// if no kind/query/receiver filters are set and exported_only is false,
		// default it to true to avoid flooding with unexported symbols.
		exportedOnly := args.ExportedOnly
		if !args.ExportedOnly && args.Query == "" {
			exportedOnly = true
		}

		params := index.SearchParams{
			Query:            args.Query,
			Kind:             args.Kind,
			Repo:             args.Repo,
			Package:          args.Package,
			ExportedOnly:     exportedOnly,
			Receiver:         args.Receiver,
			IncludeGenerated: args.IncludeGenerated,
			Limit:            args.Limit,
		}

		idx := holder.Get()
		results := idx.SearchSymbols(params)

		// Count distinct repos in results for coverage info.
		matchedRepos := make(map[string]bool)
		for _, s := range results {
			matchedRepos[s.Repo] = true
		}

		var b strings.Builder
		totalRepos := idx.RepoCount()
		fmt.Fprintf(&b, "Found %d symbols (matched %d of %d repos)\n\n", len(results), len(matchedRepos), totalRepos)

		for _, s := range results {
			kind := strings.ToUpper(string(s.Kind))
			if s.Kind == index.SymbolType {
				kind = strings.ToUpper(string(s.TypeKind))
			}

			sig := s.Signature
			if s.Kind == index.SymbolConst || s.Kind == index.SymbolVar {
				sig = s.Signature
			}

			fmt.Fprintf(&b, "[%s] %s.%s %s\n", kind, s.PkgName, s.Name, sig)
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
