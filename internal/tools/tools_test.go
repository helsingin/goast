package tools

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "sample_repo")
}

func buildTestIndex(t *testing.T) *index.Index {
	t.Helper()
	cfg := index.IndexConfig{
		Repos: []index.RepoConfig{{Path: testdataDir()}},
	}
	idx, err := index.BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return idx
}

func callTool(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	ctx := context.Background()

	idx := buildTestIndex(t)
	cfg := index.IndexConfig{
		Repos: []index.RepoConfig{{Path: testdataDir()}},
	}
	holder := index.NewHolder(idx, cfg)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	RegisterListPackages(server, holder)
	RegisterSearchSymbols(server, holder)
	RegisterReadSymbol(server, holder)
	RegisterFindImplementations(server, holder)
	RegisterFindReferences(server, holder)
	RegisterListServices(server, holder)
	RegisterListDependencies(server, holder)
	RegisterSearchConfig(server, holder)
	RegisterCrossReference(server, holder)
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

func TestListPackages_NoFilter(t *testing.T) {
	text := callTool(t, "list-packages", nil)

	if !strings.Contains(text, "greeter") {
		t.Error("expected greeter package in output")
	}
	if !strings.Contains(text, "Found") {
		t.Error("expected 'Found' header in output")
	}
	// Internal packages are included by default so the agent doesn't go
	// hunting for a nonexistent list-internal tool.
	if !strings.Contains(text, "secret") {
		t.Error("expected internal package 'secret' to be included by default")
	}
}

func TestListPackages_ExcludeInternal(t *testing.T) {
	text := callTool(t, "list-packages", map[string]any{
		"exclude_internal": true,
	})

	if strings.Contains(text, "secret") {
		t.Error("expected internal package 'secret' to be hidden when exclude_internal=true")
	}
}

func TestSearchSymbols_Query(t *testing.T) {
	text := callTool(t, "search-symbols", map[string]any{
		"query": "Greeter",
	})

	if !strings.Contains(text, "Greeter") {
		t.Error("expected Greeter in search results")
	}
	if !strings.Contains(text, "example.com/sample/pkg/greeter") {
		t.Error("expected import path in output")
	}
}

func TestSearchSymbols_KindFilter(t *testing.T) {
	text := callTool(t, "search-symbols", map[string]any{
		"query": "Greeter",
		"kind":  "struct",
	})

	if !strings.Contains(text, "STRUCT") {
		t.Error("expected STRUCT kind in output")
	}
	if strings.Contains(text, "[METHOD]") {
		t.Error("expected no METHOD results when kind=struct")
	}
}

func TestSearchSymbols_InterfaceKind(t *testing.T) {
	text := callTool(t, "search-symbols", map[string]any{
		"query": "Speaker",
		"kind":  "interface",
	})

	if !strings.Contains(text, "Speaker") {
		t.Error("expected Speaker interface in results")
	}
}

func TestSearchSymbols_IncludeGenerated(t *testing.T) {
	// Without include_generated, ProtoMessage should not appear.
	text := callTool(t, "search-symbols", map[string]any{
		"query": "ProtoMessage",
	})
	if strings.Contains(text, "ProtoMessage") {
		t.Error("expected ProtoMessage excluded by default")
	}

	// With include_generated.
	text = callTool(t, "search-symbols", map[string]any{
		"query":             "ProtoMessage",
		"include_generated": true,
	})
	if !strings.Contains(text, "ProtoMessage") {
		t.Error("expected ProtoMessage when include_generated=true")
	}
}

func TestReadSymbol_Found(t *testing.T) {
	text := callTool(t, "read-symbol", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "NewGreeter",
	})

	if !strings.Contains(text, "func NewGreeter") {
		t.Errorf("expected func NewGreeter in source, got:\n%s", text)
	}
	if !strings.Contains(text, "return &Greeter{Prefix: prefix}") {
		t.Error("expected function body in source")
	}
	if !strings.Contains(text, "// File:") {
		t.Error("expected file header")
	}
}

func TestReadSymbol_Method(t *testing.T) {
	text := callTool(t, "read-symbol", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "Greeter.Greet",
	})

	if !strings.Contains(text, "func (g *Greeter) Greet") {
		t.Errorf("expected method source, got:\n%s", text)
	}
}

func TestReadSymbol_NotFound(t *testing.T) {
	text := callTool(t, "read-symbol", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "NonExistent",
	})

	if !strings.Contains(text, "not found") {
		t.Error("expected 'not found' message")
	}
}

func TestReadSymbol_WithImports(t *testing.T) {
	text := callTool(t, "read-symbol", map[string]any{
		"package":         "example.com/sample/pkg/greeter",
		"name":            "NewGreeter",
		"include_imports": true,
	})

	if !strings.Contains(text, "import") {
		t.Error("expected import block when include_imports=true")
	}
}

func TestReadSymbol_DocComment(t *testing.T) {
	text := callTool(t, "read-symbol", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "FormatGreeting",
	})

	if !strings.Contains(text, "FormatGreeting formats a greeting string") {
		t.Errorf("expected doc comment in output, got:\n%s", text)
	}
}

// --- Fix 1: query should be optional at the MCP tool level ---

func TestSearchSymbols_NoQuery_KindFilter(t *testing.T) {
	// "list all interfaces" — no query field at all, just kind
	text := callTool(t, "search-symbols", map[string]any{
		"kind": "interface",
	})
	if !strings.Contains(text, "Speaker") {
		t.Errorf("expected Speaker when searching kind=interface with no query, got:\n%s", text)
	}
}

func TestSearchSymbols_NoQuery_RepoFilter(t *testing.T) {
	// "list all exported symbols in sample_repo"
	text := callTool(t, "search-symbols", map[string]any{
		"repo": "sample_repo",
	})
	if !strings.Contains(text, "Found") {
		t.Error("expected results header")
	}
	if !strings.Contains(text, "Greeter") {
		t.Error("expected Greeter in repo-filtered results")
	}
}

// --- Fix 2: query matches doc and import path at tool level ---

func TestSearchSymbols_DocMatch(t *testing.T) {
	// "personalized" is in Greeter's doc but not its name
	text := callTool(t, "search-symbols", map[string]any{
		"query": "personalized",
	})
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter via doc match on 'personalized', got:\n%s", text)
	}
}

// --- Repo coverage in search output ---

func TestSearchSymbols_ShowsRepoCoverage(t *testing.T) {
	// Output should show how many repos matched so agents know if they need
	// to try related terms for broader coverage.
	text := callTool(t, "search-symbols", map[string]any{
		"query": "Greeter",
	})
	// With only 1 test repo, should show "matched 1 repo" or similar.
	if !strings.Contains(text, "1 repo") {
		t.Errorf("expected repo coverage info in output, got:\n%s", text)
	}
}

// --- find-implementations tool tests ---

func TestFindImplementations_Speaker(t *testing.T) {
	text := callTool(t, "find-implementations", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "Speaker",
	})
	for _, want := range []string{"LoudSpeaker", "HappySpeaker", "MuteLoudSpeaker"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %s in implementations of Speaker, got:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "found 3") {
		t.Errorf("expected 'found 3' in output, got:\n%s", text)
	}
}

func TestFindImplementations_Reverse(t *testing.T) {
	text := callTool(t, "find-implementations", map[string]any{
		"package":   "example.com/sample/pkg/greeter",
		"name":      "LoudSpeaker",
		"direction": "interfaces",
	})
	if !strings.Contains(text, "Speaker") {
		t.Errorf("expected Speaker in interfaces for LoudSpeaker, got:\n%s", text)
	}
	if !strings.Contains(text, "found 1") {
		t.Errorf("expected 'found 1' in output, got:\n%s", text)
	}
}

func TestFindReferences_NewGreeter(t *testing.T) {
	text := callTool(t, "find-references", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "NewGreeter",
	})
	if !strings.Contains(text, "found") {
		t.Errorf("expected 'found' header, got:\n%s", text)
	}
	if !strings.Contains(text, "Run") {
		t.Errorf("expected caller Run in output, got:\n%s", text)
	}
	if !strings.Contains(text, "example.com/sample/pkg/app") {
		t.Errorf("expected app package in output, got:\n%s", text)
	}
}

func TestFindReferences_NoResults(t *testing.T) {
	text := callTool(t, "find-references", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "Nonexistent",
	})
	if !strings.Contains(text, "found 0") {
		t.Errorf("expected 'found 0' for unknown symbol, got:\n%s", text)
	}
}

func TestFindImplementations_NotAnInterface(t *testing.T) {
	text := callTool(t, "find-implementations", map[string]any{
		"package": "example.com/sample/pkg/greeter",
		"name":    "Greeter",
	})
	// Greeter is a struct, not an interface — should find 0 implementations
	if !strings.Contains(text, "found 0") {
		t.Errorf("expected 'found 0' for non-interface, got:\n%s", text)
	}
}

func TestFindImplementations_InvalidDirection(t *testing.T) {
	text := callTool(t, "find-implementations", map[string]any{
		"package":   "example.com/sample/pkg/greeter",
		"name":      "Speaker",
		"direction": "invalid",
	})
	if !strings.Contains(text, "Invalid direction") {
		t.Errorf("expected error for invalid direction, got:\n%s", text)
	}
}

// --- list-services tool tests ---

func TestListServices_All(t *testing.T) {
	text := callTool(t, "list-services", nil)
	if !strings.Contains(text, "Found 1 gRPC service") {
		t.Errorf("expected 'Found 1 gRPC service', got:\n%s", text)
	}
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter service in output, got:\n%s", text)
	}
	if !strings.Contains(text, "SayHello") {
		t.Errorf("expected SayHello method in output, got:\n%s", text)
	}
}

func TestListServices_RepoFilter(t *testing.T) {
	text := callTool(t, "list-services", map[string]any{
		"repo": "nonexistent",
	})
	if !strings.Contains(text, "Found 0 gRPC services") {
		t.Errorf("expected 0 services for nonexistent repo, got:\n%s", text)
	}
}

// --- list-dependencies tool tests ---

func TestListDependencies_SingleRepo(t *testing.T) {
	// With only 1 test repo, there should be 0 cross-repo dependencies.
	text := callTool(t, "list-dependencies", nil)
	if !strings.Contains(text, "Found 0 cross-repo dependencies") {
		t.Errorf("expected 0 dependencies for single repo, got:\n%s", text)
	}
}

// --- search-config tool tests ---

func TestSearchConfig_ByFieldName(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"query": "Prefix",
	})
	if !strings.Contains(text, "Prefix") {
		t.Errorf("expected Prefix field in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter struct context, got:\n%s", text)
	}
}

func TestSearchConfig_ByTag(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"query": "omitempty",
	})
	if !strings.Contains(text, "Suffix") {
		t.Errorf("expected Suffix field (has omitempty tag), got:\n%s", text)
	}
}

func TestSearchConfig_RepoFilter(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"repo": "nonexistent",
	})
	if !strings.Contains(text, "Found 0 fields") {
		t.Errorf("expected 0 fields for nonexistent repo, got:\n%s", text)
	}
}

func TestSearchConfig_GroupedByStruct(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"query": "string",
	})
	// Should group fields by struct — Greeter should appear once with both Prefix and Suffix
	if !strings.Contains(text, "[STRUCT] Greeter") {
		t.Errorf("expected grouped struct header, got:\n%s", text)
	}
}

func TestSearchConfig_ShowsTagKey(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"query": "Prefix",
	})
	// Prefix has json:"prefix" tag — output should show json:prefix
	if !strings.Contains(text, "json:prefix") {
		t.Errorf("expected parsed json tag key in output, got:\n%s", text)
	}
}

func TestSearchConfig_ShowsDefaults(t *testing.T) {
	text := callTool(t, "search-config", map[string]any{
		"query": "Port",
	})
	// GRPCConfig.Port defaults to 50051, HTTPConfig.Port defaults to 8080
	if !strings.Contains(text, "[default: 50051]") {
		t.Errorf("expected [default: 50051] for GRPCConfig.Port, got:\n%s", text)
	}
	if !strings.Contains(text, "[default: 8080]") {
		t.Errorf("expected [default: 8080] for HTTPConfig.Port, got:\n%s", text)
	}
}

func TestSearchConfig_NestedDefaults(t *testing.T) {
	// Nested struct defaults should propagate — GRPCConfig.Host should show
	// the default from DefaultGreeterConfig()'s nested GRPCConfig{Host: "127.0.0.1"}.
	text := callTool(t, "search-config", map[string]any{
		"query": "host",
	})
	if !strings.Contains(text, `[default: "127.0.0.1"]`) {
		t.Errorf("expected nested default for GRPCConfig.Host, got:\n%s", text)
	}
}

func TestSearchConfig_DefaultsForAllFields(t *testing.T) {
	// Search by yaml tag which all config fields have.
	text := callTool(t, "search-config", map[string]any{
		"query": "yaml",
	})
	if !strings.Contains(text, `[default: "127.0.0.1"]`) {
		t.Errorf("expected default for GRPCConfig.Host, got:\n%s", text)
	}
	if !strings.Contains(text, "[default: 5000]") {
		t.Errorf("expected default for HTTPConfig.ReadTimeoutMS, got:\n%s", text)
	}
	if !strings.Contains(text, "[default: false]") {
		t.Errorf("expected default for Verbose, got:\n%s", text)
	}
}

// --- Reindex tool tests ---

func TestReindex_RebuildsIndex(t *testing.T) {
	text := callTool(t, "reindex", nil)

	if !strings.Contains(text, "Reindex complete") {
		t.Errorf("expected 'Reindex complete' in output, got:\n%s", text)
	}
	if !strings.Contains(text, "symbols") {
		t.Errorf("expected symbol count in output, got:\n%s", text)
	}
	if !strings.Contains(text, "packages") {
		t.Errorf("expected package count in output, got:\n%s", text)
	}
}

func TestReindex_ToolsUseUpdatedIndex(t *testing.T) {
	// Verify that after reindex, other tools see the rebuilt index.
	// We call reindex then search — both should work through the holder.
	ctx := context.Background()

	idx := buildTestIndex(t)
	cfg := index.IndexConfig{
		Repos: []index.RepoConfig{{Path: testdataDir()}},
	}
	holder := index.NewHolder(idx, cfg)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	RegisterListPackages(server, holder)
	RegisterSearchSymbols(server, holder)
	RegisterReadSymbol(server, holder)
	RegisterFindImplementations(server, holder)
	RegisterFindReferences(server, holder)
	RegisterListServices(server, holder)
	RegisterListDependencies(server, holder)
	RegisterSearchConfig(server, holder)
	RegisterCrossReference(server, holder)
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

	// Call reindex first.
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "reindex",
	})
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}

	// Now search should still work with the new index.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search-symbols",
		Arguments: map[string]any{"query": "Greeter"},
	})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}

	var text string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter after reindex, got:\n%s", text)
	}
}

// --- cross-reference tool tests ---

func TestCrossRef_Unimplemented_Speaker(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode":      "unimplemented",
		"interface": "Speaker",
		"package":   "example.com/sample/pkg/greeter",
	})
	// LoudSpeaker implements Speaker, others don't.
	if !strings.Contains(text, "NOT IMPLEMENTING") {
		t.Errorf("expected NOT IMPLEMENTING section, got:\n%s", text)
	}
	if !strings.Contains(text, "IMPLEMENTING") {
		t.Errorf("expected IMPLEMENTING section, got:\n%s", text)
	}
	if !strings.Contains(text, "LoudSpeaker") {
		t.Errorf("expected LoudSpeaker in IMPLEMENTING section, got:\n%s", text)
	}
	// Greeter should be in NOT IMPLEMENTING.
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter in NOT IMPLEMENTING section, got:\n%s", text)
	}
	// Header should show counts.
	if !strings.Contains(text, "Types NOT implementing Speaker") {
		t.Errorf("expected header with interface name, got:\n%s", text)
	}
}

func TestCrossRef_Unimplemented_WithNameFilter(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode":        "unimplemented",
		"interface":   "Speaker",
		"package":     "example.com/sample/pkg/greeter",
		"name_filter": "Config",
	})
	// Only Config structs should appear.
	if !strings.Contains(text, "GreeterConfig") {
		t.Errorf("expected GreeterConfig in filtered results, got:\n%s", text)
	}
	if !strings.Contains(text, "GRPCConfig") {
		t.Errorf("expected GRPCConfig in filtered results, got:\n%s", text)
	}
	// Non-Config structs should be excluded.
	if strings.Contains(text, "LoudSpeaker") {
		t.Errorf("expected LoudSpeaker excluded by name_filter, got:\n%s", text)
	}
	if !strings.Contains(text, `matching "Config"`) {
		t.Errorf("expected name filter in header, got:\n%s", text)
	}
}

func TestCrossRef_MissingField_Port(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode":        "missing-field",
		"field":       "Port",
		"name_filter": "Config",
	})
	// GRPCConfig and HTTPConfig have Port; GreeterConfig doesn't.
	if !strings.Contains(text, "MISSING") {
		t.Errorf("expected MISSING section, got:\n%s", text)
	}
	if !strings.Contains(text, "PRESENT") {
		t.Errorf("expected PRESENT section, got:\n%s", text)
	}
	if !strings.Contains(text, "GreeterConfig") {
		t.Errorf("expected GreeterConfig in MISSING section, got:\n%s", text)
	}
	if !strings.Contains(text, "GRPCConfig") {
		t.Errorf("expected GRPCConfig in PRESENT section, got:\n%s", text)
	}
	if !strings.Contains(text, "HTTPConfig") {
		t.Errorf("expected HTTPConfig in PRESENT section, got:\n%s", text)
	}
	// Missing section should show GreeterConfig's fields for context.
	if !strings.Contains(text, "Fields:") {
		t.Errorf("expected field summary in MISSING section, got:\n%s", text)
	}
}

func TestCrossRef_FieldCoverage_Port(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode":        "field-coverage",
		"field":       "Port",
		"name_filter": "Config",
	})
	// Should show coverage report.
	if !strings.Contains(text, `Field "Port" coverage:`) {
		t.Errorf("expected coverage header, got:\n%s", text)
	}
	if !strings.Contains(text, `WITH "Port"`) {
		t.Errorf("expected WITH section, got:\n%s", text)
	}
	if !strings.Contains(text, `WITHOUT "Port"`) {
		t.Errorf("expected WITHOUT section, got:\n%s", text)
	}
	// Should show defaults for Port fields.
	if !strings.Contains(text, "[default: 50051]") {
		t.Errorf("expected default value for GRPCConfig.Port, got:\n%s", text)
	}
	if !strings.Contains(text, "[default: 8080]") {
		t.Errorf("expected default value for HTTPConfig.Port, got:\n%s", text)
	}
}

func TestCrossRef_UnimplementedServices(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode": "unimplemented-services",
	})
	// GreeterServiceServer has no concrete impl in testdata.
	if !strings.Contains(text, "UNIMPLEMENTED") {
		t.Errorf("expected UNIMPLEMENTED section, got:\n%s", text)
	}
	if !strings.Contains(text, "Greeter") {
		t.Errorf("expected Greeter service in output, got:\n%s", text)
	}
	if !strings.Contains(text, "gRPC services WITHOUT concrete implementation") {
		t.Errorf("expected header, got:\n%s", text)
	}
}

func TestCrossRef_InvalidMode(t *testing.T) {
	text := callTool(t, "cross-reference", map[string]any{
		"mode": "invalid",
	})
	if !strings.Contains(text, "Invalid mode") {
		t.Errorf("expected error for invalid mode, got:\n%s", text)
	}
}

func TestCrossRef_MissingRequiredParams(t *testing.T) {
	// unimplemented without interface/package.
	text := callTool(t, "cross-reference", map[string]any{
		"mode": "unimplemented",
	})
	if !strings.Contains(text, "requires") {
		t.Errorf("expected error for missing params, got:\n%s", text)
	}

	// missing-field without field.
	text = callTool(t, "cross-reference", map[string]any{
		"mode": "missing-field",
	})
	if !strings.Contains(text, "requires") {
		t.Errorf("expected error for missing field param, got:\n%s", text)
	}

	// field-coverage without field.
	text = callTool(t, "cross-reference", map[string]any{
		"mode": "field-coverage",
	})
	if !strings.Contains(text, "requires") {
		t.Errorf("expected error for missing field param, got:\n%s", text)
	}
}
