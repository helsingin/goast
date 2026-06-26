package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchConfigArgs are the input arguments for the search-config tool.
type SearchConfigArgs struct {
	Query   string `json:"query,omitempty" jsonschema:"Search query: substring match on field name, Go type, struct tag (yaml/json keys), and doc comment. Optional — omit to list all struct fields matching other filters."`
	Repo    string `json:"repo,omitempty" jsonschema:"Filter by repository name (e.g. service-api)"`
	Package string `json:"package,omitempty" jsonschema:"Filter by import path or prefix (e.g. github.com/acme/platform/api/internal/config)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max results to return (default: no limit — returns all matching fields)"`
}

// parseTagKey extracts the first key value from a struct tag for the given tag name.
// e.g. parseTagKey(`json:"prefix,omitempty" yaml:"prefix"`, "yaml") returns "prefix".
func parseTagKey(tag, tagName string) string {
	// Look for tagName:"..."
	prefix := tagName + `:`
	i := strings.Index(tag, prefix)
	if i < 0 {
		return ""
	}
	rest := tag[i+len(prefix):]
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	val := rest[:end]
	// Strip options like ",omitempty"
	if comma := strings.Index(val, ","); comma >= 0 {
		val = val[:comma]
	}
	return val
}

// maxOutputSize caps the rendered output to avoid exceeding MCP client token limits.
// Set conservatively to account for JSON encoding overhead (newline escaping, etc.).
const maxOutputSize = 50000

// RegisterSearchConfig registers the search-config tool on the server.
func RegisterSearchConfig(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search-config",
		Description: `Search across struct fields in all repos. Finds config fields by name, Go type, struct tag (yaml/json keys), or doc comment. Use this to answer cross-service config questions like "which services have NATS config", "what ports do services use", "show me all timeout fields". Query matches against field name, type, tag, and doc (case-insensitive substring). Omit query and use repo/package filters to list all fields for a specific service's config. If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SearchConfigArgs) (*mcp.CallToolResult, any, error) {
		idx := holder.Get()
		results := idx.SearchFields(index.FieldSearchParams{
			Query:   args.Query,
			Repo:    args.Repo,
			Package: args.Package,
			Limit:   args.Limit,
		})

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d fields", len(results))
		if args.Query != "" {
			fmt.Fprintf(&b, " matching %q", args.Query)
		}
		if args.Repo != "" {
			fmt.Fprintf(&b, " in %s", args.Repo)
		}
		fmt.Fprintf(&b, "\n\n")

		// Group results by struct for readable output.
		type structKey struct {
			name       string
			importPath string
		}
		var order []structKey
		grouped := make(map[structKey][]index.FieldResult)
		for _, r := range results {
			key := structKey{name: r.StructName, importPath: r.StructImportPath}
			if _, exists := grouped[key]; !exists {
				order = append(order, key)
			}
			grouped[key] = append(grouped[key], r)
		}

		fieldsWritten := 0
		truncated := false

		for _, key := range order {
			fields := grouped[key]
			r := fields[0]

			// Check if adding this struct group would exceed the output cap.
			if b.Len() > maxOutputSize && fieldsWritten > 0 {
				truncated = true
				break
			}

			fmt.Fprintf(&b, "[STRUCT] %s\n", key.name)
			fmt.Fprintf(&b, "  %s | %s | %s:%d\n", key.importPath, r.Repo, filepath.Base(r.FilePath), r.Line)
			for _, f := range fields {
				yamlKey := parseTagKey(f.FieldTag, "yaml")
				jsonKey := parseTagKey(f.FieldTag, "json")
				tagDisplay := ""
				if yamlKey != "" {
					tagDisplay = fmt.Sprintf(" yaml:%s", yamlKey)
				} else if jsonKey != "" {
					tagDisplay = fmt.Sprintf(" json:%s", jsonKey)
				}
				fmt.Fprintf(&b, "    %s %s%s", f.FieldName, f.FieldType, tagDisplay)
				if f.FieldDoc != "" {
					fmt.Fprintf(&b, "  — %s", strings.TrimSpace(f.FieldDoc))
				}
				if f.FieldDefaultValue != "" {
					fmt.Fprintf(&b, " [default: %s]", f.FieldDefaultValue)
				}
				fmt.Fprintf(&b, "\n")
				fieldsWritten++
			}
			b.WriteString("\n")
		}

		if truncated {
			remaining := len(results) - fieldsWritten
			fmt.Fprintf(&b, "--- OUTPUT TRUNCATED (%d of %d fields shown, %d remaining) ---\n", fieldsWritten, len(results), remaining)
			fmt.Fprintf(&b, "Narrow your search with repo or package filters, or a more specific query.\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: b.String()},
			},
		}, nil, nil
	})
}
