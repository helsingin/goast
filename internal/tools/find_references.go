package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// FindReferencesArgs are the input arguments for the find-references tool.
type FindReferencesArgs struct {
	Package string `json:"package" jsonschema:"required,Full import path of the package containing the target symbol (e.g. github.com/acme/platform/auth)"`
	Name    string `json:"name" jsonschema:"required,Symbol name. For methods use Type.Method format (e.g. KeyManager.GetKey)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum references to return (default 200)"`
}

// RegisterFindReferences registers the find-references tool on the server.
func RegisterFindReferences(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find-references",
		Description: `Returns call sites and package-qualified value references to a KNOWN symbol (exact package + name). Covers cross-package calls, qualified value refs (e.g. ErrFoo, DefaultConfig), and same-package unqualified calls. Does NOT cover method calls on values (x.Method()) since that requires type resolution — if you need method call sites and find-references misses them, fall back to search-symbols + read-symbol. If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FindReferencesArgs) (*mcp.CallToolResult, any, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 200
		}

		idx := holder.Get()
		refs := idx.FindReferences(args.Package, args.Name)

		var b strings.Builder
		total := len(refs)
		fmt.Fprintf(&b, "References to %s.%s: found %d", args.Package, args.Name, total)
		if total > limit {
			fmt.Fprintf(&b, " (showing %d)", limit)
			refs = refs[:limit]
		}
		b.WriteString("\n\n")

		if total == 0 {
			b.WriteString("No indexed references found. Note: method calls on values (x.Method()) aren't tracked.\n")
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
			}, nil, nil
		}

		// Sort: by caller's repo, then package, then name, then line.
		sort.Slice(refs, func(i, j int) bool {
			a := idx.Symbols[refs[i].FromSymbol]
			c := idx.Symbols[refs[j].FromSymbol]
			if a.Repo != c.Repo {
				return a.Repo < c.Repo
			}
			if a.ImportPath != c.ImportPath {
				return a.ImportPath < c.ImportPath
			}
			if a.Name != c.Name {
				return a.Name < c.Name
			}
			return refs[i].Line < refs[j].Line
		})

		for _, r := range refs {
			s := idx.Symbols[r.FromSymbol]
			caller := s.Name
			if s.Receiver != "" {
				caller = s.Receiver + "." + s.Name
			}
			fmt.Fprintf(&b, "[%s] %s.%s\n", strings.ToUpper(string(s.Kind)), s.PkgName, caller)
			fmt.Fprintf(&b, "  %s | %s | %s:%d\n", s.ImportPath, s.Repo, filepath.Base(r.FilePath), r.Line)
			b.WriteString("\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
		}, nil, nil
	})
}
