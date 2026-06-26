package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func realRepoHolder(t *testing.T) *index.IndexHolder {
	t.Helper()
	repoPath := os.Getenv("GOAST_INTEGRATION_REPO")
	if repoPath == "" {
		t.Skip("set GOAST_INTEGRATION_REPO to run integration tests against a real repository")
	}
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		t.Skipf("skipping integration test: repository not found: %s", repoPath)
	}

	cfg := index.IndexConfig{
		Repos: []index.RepoConfig{
			{Path: repoPath},
		},
	}
	idx, err := index.BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return index.NewHolder(idx, cfg)
}

func callRealTool(t *testing.T, holder *index.IndexHolder, name string, args map[string]any) string {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "integration-test", Version: "0.0.1"}, nil)
	RegisterListPackages(server, holder)
	RegisterSearchSymbols(server, holder)
	RegisterReadSymbol(server, holder)
	RegisterFindImplementations(server, holder)
	RegisterFindReferences(server, holder)
	RegisterListServices(server, holder)
	RegisterListDependencies(server, holder)
	RegisterReindex(server, holder)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}

	var text string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func TestIntegration_ListPackages(t *testing.T) {
	holder := realRepoHolder(t)
	text := callRealTool(t, holder, "list-packages", nil)

	if !strings.Contains(text, "Found") {
		t.Error("expected Found header")
	}
	t.Logf("list-packages output (first 500 chars):\n%s", text[:min(500, len(text))])
}

func TestIntegration_SearchSymbols(t *testing.T) {
	holder := realRepoHolder(t)

	// Search for a common pattern.
	text := callRealTool(t, holder, "search-symbols", map[string]any{
		"query": "Service",
	})
	if !strings.Contains(text, "Found") {
		t.Error("expected Found header")
	}
	t.Logf("search 'Service' (first 500 chars):\n%s", text[:min(500, len(text))])

	// Search interfaces.
	text = callRealTool(t, holder, "search-symbols", map[string]any{
		"query": "Service",
		"kind":  "interface",
	})
	t.Logf("search 'Service' interfaces (first 500 chars):\n%s", text[:min(500, len(text))])
}

func TestIntegration_ReadSymbol(t *testing.T) {
	holder := realRepoHolder(t)
	idx := holder.Get()

	// First search to find a symbol.
	results := idx.SearchSymbols(index.SearchParams{
		Query:        "New",
		Kind:         "func",
		ExportedOnly: true,
		Limit:        1,
	})
	if len(results) == 0 {
		t.Skip("no results found for 'New' funcs")
	}

	sym := results[0]
	text := callRealTool(t, holder, "read-symbol", map[string]any{
		"package": sym.ImportPath,
		"name":    sym.Name,
	})
	if !strings.Contains(text, "func") {
		t.Error("expected func in read-symbol output")
	}
	t.Logf("read-symbol %s.%s (first 500 chars):\n%s", sym.ImportPath, sym.Name, text[:min(500, len(text))])
}
