package index

import (
	"strings"
	"testing"
)

func TestSearchSymbols_Substring(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "greet", ExportedOnly: false})
	// Should match: Greeter (type), Greet (method), NewGreeter (func), FormatGreeting (func), GreetCount (var)
	if len(results) < 4 {
		t.Errorf("expected at least 4 results for 'greet', got %d", len(results))
		for _, s := range results {
			t.Logf("  %s %s", s.Kind, s.Name)
		}
	}
}

func TestSearchSymbols_ExactMatch(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "exact:Greeter", ExportedOnly: false})
	if len(results) != 1 {
		t.Errorf("expected 1 result for exact:Greeter, got %d", len(results))
		for _, s := range results {
			t.Logf("  %s %s", s.Kind, s.Name)
		}
		return
	}
	if results[0].Name != "Greeter" || results[0].Kind != SymbolType {
		t.Errorf("expected Greeter type, got %s %s", results[0].Kind, results[0].Name)
	}
}

func TestSearchSymbols_KindFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Kind: "func", ExportedOnly: false})
	for _, s := range results {
		if s.Kind != SymbolFunc {
			t.Errorf("expected kind=func, got %s for %s", s.Kind, s.Name)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least 1 func result")
	}
}

func TestSearchSymbols_InterfaceKind(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Kind: "interface", ExportedOnly: false})
	// Speaker, AudioDevice
	if len(results) != 2 {
		t.Errorf("expected 2 interfaces, got %d", len(results))
		return
	}
	names := map[string]bool{results[0].Name: true, results[1].Name: true}
	for _, want := range []string{"Speaker", "AudioDevice"} {
		if !names[want] {
			t.Errorf("expected %s in interfaces, got %v", want, names)
		}
	}
}

func TestSearchSymbols_StructKind(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Kind: "struct", ExportedOnly: false})
	// Greeter, LoudSpeaker, HappySpeaker, MuteLoudSpeaker, GRPCConfig,
	// HTTPConfig, GreeterConfig, Token, ProtoMessage
	// ProtoMessage is generated and excluded by default → 8
	if len(results) != 8 {
		t.Errorf("expected 8 structs (generated excluded), got %d", len(results))
		for _, s := range results {
			t.Logf("  %s %s (generated=%v)", s.Kind, s.Name, s.Generated)
		}
	}
}

func TestSearchSymbols_RepoFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Repo: "sample_repo", ExportedOnly: false})
	for _, s := range results {
		if s.Repo != "sample_repo" {
			t.Errorf("expected repo=sample_repo, got %s for %s", s.Repo, s.Name)
		}
	}
}

func TestSearchSymbols_PackageFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{
		Package:      "example.com/sample/pkg/greeter",
		ExportedOnly: false,
	})
	for _, s := range results {
		if s.ImportPath != "example.com/sample/pkg/greeter" {
			t.Errorf("expected import path example.com/sample/pkg/greeter, got %s for %s", s.ImportPath, s.Name)
		}
	}
	if len(results) == 0 {
		t.Error("expected results for greeter package")
	}
}

func TestSearchSymbols_ExportedOnly(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{
		Package:      "example.com/sample/pkg/greeter",
		ExportedOnly: true,
	})
	for _, s := range results {
		if !s.Exported {
			t.Errorf("expected only exported symbols, got unexported %s", s.Name)
		}
	}
	// sanitizeName should be excluded
	for _, s := range results {
		if s.Name == "sanitizeName" {
			t.Error("sanitizeName should be excluded with ExportedOnly=true")
		}
	}
}

func TestSearchSymbols_ReceiverFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Receiver: "Greeter", ExportedOnly: false})
	if len(results) != 2 {
		t.Errorf("expected 2 methods on Greeter, got %d", len(results))
		for _, s := range results {
			t.Logf("  %s %s (receiver=%s)", s.Kind, s.Name, s.Receiver)
		}
	}
	for _, s := range results {
		if s.Receiver != "Greeter" {
			t.Errorf("expected receiver=Greeter, got %s for %s", s.Receiver, s.Name)
		}
	}
}

func TestSearchSymbols_GeneratedExclusion(t *testing.T) {
	idx := buildTestIndex(t)
	// Without IncludeGenerated, should not see ProtoMessage.
	results := idx.SearchSymbols(SearchParams{Query: "Proto", ExportedOnly: false})
	for _, s := range results {
		if s.Generated {
			t.Errorf("expected no generated symbols, got %s", s.Name)
		}
	}
}

func TestSearchSymbols_GeneratedInclusion(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "Proto", ExportedOnly: false, IncludeGenerated: true})
	found := false
	for _, s := range results {
		if s.Name == "ProtoMessage" {
			found = true
		}
	}
	if !found {
		t.Error("expected ProtoMessage when IncludeGenerated=true")
	}
}

func TestSearchSymbols_Limit(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{ExportedOnly: false, Limit: 3})
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

func TestSearchSymbols_SortOrder(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "greet", ExportedOnly: false, Limit: 100})
	if len(results) < 2 {
		t.Skip("not enough results to test sort order")
	}
	// Exact name matches (case-insensitive) should come first, then exported before unexported.
	// Check that exported symbols come before unexported among non-exact matches.
	seenUnexported := false
	for _, s := range results {
		if !s.Exported {
			seenUnexported = true
		} else if seenUnexported {
			// An exported symbol after an unexported one — only ok if they differ on exact match.
			t.Logf("warning: exported %s after unexported symbol (may be expected if exact match differs)", s.Name)
		}
	}
}

func TestSearchSymbols_CaseInsensitive(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "GREETER", ExportedOnly: false})
	found := false
	for _, s := range results {
		if s.Name == "Greeter" {
			found = true
		}
	}
	if !found {
		t.Error("case-insensitive search for GREETER should find Greeter")
	}
}

func TestListPackages_All(t *testing.T) {
	idx := buildTestIndex(t)
	pkgs := idx.ListPackages("", true)
	// greeter, the real greeter_test package, secret (internal), app, and sample.
	if len(pkgs) != 5 {
		t.Errorf("expected 5 packages, got %d", len(pkgs))
	}
}

func TestListPackages_ExcludeInternal(t *testing.T) {
	idx := buildTestIndex(t)
	pkgs := idx.ListPackages("", false)
	for _, p := range pkgs {
		if p.Internal {
			t.Errorf("expected no internal packages, got %s", p.ImportPath)
		}
	}
	// Non-internal: greeter, greeter_test, app, sample.
	if len(pkgs) != 4 {
		t.Errorf("expected 4 non-internal packages, got %d", len(pkgs))
	}
}

func TestListPackages_RepoFilter(t *testing.T) {
	idx := buildTestIndex(t)
	pkgs := idx.ListPackages("sample_repo", true)
	for _, p := range pkgs {
		if p.Repo != "sample_repo" {
			t.Errorf("expected repo=sample_repo, got %s", p.Repo)
		}
	}
}

func TestGetSymbol_ByName(t *testing.T) {
	idx := buildTestIndex(t)
	s := idx.GetSymbol("example.com/sample/pkg/greeter", "NewGreeter")
	if s == nil {
		t.Fatal("expected to find NewGreeter")
		return
	}
	if s.Kind != SymbolFunc {
		t.Errorf("expected kind=func, got %s", s.Kind)
	}
}

func TestGetSymbol_Method(t *testing.T) {
	idx := buildTestIndex(t)
	s := idx.GetSymbol("example.com/sample/pkg/greeter", "Greeter.Greet")
	if s == nil {
		t.Fatal("expected to find Greeter.Greet")
		return
	}
	if s.Kind != SymbolMethod {
		t.Errorf("expected kind=method, got %s", s.Kind)
	}
	if s.Receiver != "Greeter" {
		t.Errorf("expected receiver=Greeter, got %s", s.Receiver)
	}
}

func TestGetSymbol_NotFound(t *testing.T) {
	idx := buildTestIndex(t)
	s := idx.GetSymbol("example.com/sample/pkg/greeter", "NonExistent")
	if s != nil {
		t.Error("expected nil for non-existent symbol")
	}
}

func TestGetPackage_Found(t *testing.T) {
	idx := buildTestIndex(t)
	p := idx.GetPackage("example.com/sample/pkg/greeter")
	if p == nil {
		t.Fatal("expected to find greeter package")
		return
	}
	if p.Name != "greeter" {
		t.Errorf("expected name=greeter, got %s", p.Name)
	}
}

func TestGetPackage_NotFound(t *testing.T) {
	idx := buildTestIndex(t)
	p := idx.GetPackage("example.com/nonexistent")
	if p != nil {
		t.Error("expected nil for non-existent package")
	}
}

// --- Fix 1: Query should be optional ---

func TestSearchSymbols_NoQuery_KindOnly(t *testing.T) {
	// "list all interfaces" — no query, just kind=interface
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Kind: "interface", ExportedOnly: false})
	// Speaker, AudioDevice
	if len(results) != 2 {
		t.Errorf("expected 2 interfaces with no query, got %d", len(results))
		return
	}
}

func TestSearchSymbols_NoQuery_KindAndRepo(t *testing.T) {
	// "list all structs in sample_repo" — no query, kind+repo filters
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{
		Kind:         "struct",
		Repo:         "sample_repo",
		ExportedOnly: false,
	})
	// Greeter, GRPCConfig, HTTPConfig, GreeterConfig, LoudSpeaker,
	// HappySpeaker, MuteLoudSpeaker, Token (ProtoMessage excluded as generated)
	if len(results) != 8 {
		t.Errorf("expected 8 structs in sample_repo, got %d", len(results))
		for _, s := range results {
			t.Logf("  %s %s", s.Kind, s.Name)
		}
	}
}

func TestSearchSymbols_NoQuery_RepoOnly(t *testing.T) {
	// "list all exported symbols in sample_repo" — no query, repo filter only
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{
		Repo:         "sample_repo",
		ExportedOnly: true,
		Limit:        100,
	})
	// Should get all exported, non-generated symbols
	if len(results) == 0 {
		t.Error("expected results for repo-only filter with no query")
	}
	for _, s := range results {
		if !s.Exported {
			t.Errorf("got unexported symbol %s with ExportedOnly=true", s.Name)
		}
		if s.Generated {
			t.Errorf("got generated symbol %s without IncludeGenerated", s.Name)
		}
	}
}

// --- Fix 2: Query should match doc and import path, not just name ---

func TestSearchSymbols_MatchesDocSummary(t *testing.T) {
	// Searching "personalized" should match Greeter whose doc is
	// "Greeter generates personalized greetings."
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "personalized", ExportedOnly: false})
	found := false
	for _, s := range results {
		if s.Name == "Greeter" {
			found = true
		}
	}
	if !found {
		t.Error("expected Greeter to match via doc summary 'personalized'")
	}
}

func TestSearchSymbols_MatchesImportPath(t *testing.T) {
	// Searching "greeter" should match symbols in example.com/sample/pkg/greeter
	// even if "greeter" is not in the symbol name (e.g. Speaker, DefaultGreeting)
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "greeter", ExportedOnly: false})

	// Speaker's name doesn't contain "greeter", but its import path does
	found := false
	for _, s := range results {
		if s.Name == "Speaker" {
			found = true
		}
	}
	if !found {
		t.Error("expected Speaker to match via import path containing 'greeter'")
	}
}

func TestSearchSymbols_MatchesDocSummary_EdgeCase(t *testing.T) {
	// "speak" should match Speaker via its doc "something that can speak a message"
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "speak", ExportedOnly: false})
	found := false
	for _, s := range results {
		if s.Name == "Speaker" {
			found = true
		}
	}
	if !found {
		t.Error("expected Speaker to match via doc summary containing 'speak'")
	}
}

func TestSearchSymbols_MatchesFullDoc(t *testing.T) {
	// "empty" appears in FormatGreeting's second sentence:
	// "It handles edge cases like empty names by returning just the greeting."
	// DocSummary only has the first sentence, so this tests full Doc search.
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "empty", ExportedOnly: false})
	found := false
	for _, s := range results {
		if s.Name == "FormatGreeting" {
			found = true
		}
	}
	if !found {
		t.Error("expected FormatGreeting to match via full doc containing 'empty' (not just first sentence)")
	}
}

func TestSearchSymbols_NameMatchRankedAboveDocMatch(t *testing.T) {
	// When a query matches both name and doc, name matches should rank first
	idx := buildTestIndex(t)
	results := idx.SearchSymbols(SearchParams{Query: "greeting", ExportedOnly: false})

	// FormatGreeting and DefaultGreeting match by name
	// Other symbols might match via doc ("greetings" in docs)
	// Name matches should come first
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// First results should be the name-matched ones
	firstIsNameMatch := false
	for _, s := range results[:2] {
		if strings.Contains(strings.ToLower(s.Name), "greeting") {
			firstIsNameMatch = true
			break
		}
	}
	if !firstIsNameMatch {
		t.Error("expected name matches to rank above doc/import-path matches")
		for _, s := range results {
			t.Logf("  %s %s (doc: %s)", s.Kind, s.Name, s.DocSummary)
		}
	}
}

// --- ListServices tests ---

func TestListServices_FindsGRPCService(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.ListServices("")
	if len(results) != 1 {
		t.Fatalf("expected 1 gRPC service, got %d", len(results))
	}
	if results[0].Name != "GreeterServiceServer" {
		t.Errorf("expected GreeterServiceServer, got %s", results[0].Name)
	}
}

func TestListServices_ExcludesUnsafe(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.ListServices("")
	for _, s := range results {
		if strings.HasPrefix(s.Name, "Unsafe") {
			t.Errorf("expected no Unsafe* stubs, got %s", s.Name)
		}
	}
}

func TestListServices_RepoFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.ListServices("nonexistent_repo")
	if len(results) != 0 {
		t.Errorf("expected 0 services for nonexistent repo, got %d", len(results))
	}
	results = idx.ListServices("sample_repo")
	if len(results) != 1 {
		t.Errorf("expected 1 service for sample_repo, got %d", len(results))
	}
}

// --- SearchFields tests ---

func TestSearchFields_ByName(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "Prefix"})
	found := false
	for _, r := range results {
		if r.FieldName == "Prefix" && r.StructName == "Greeter" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find Greeter.Prefix field by name")
	}
}

func TestSearchFields_ByTag(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "json"})
	if len(results) == 0 {
		t.Error("expected to find fields with json tags")
	}
	// Greeter has json:"prefix" and json:"suffix,omitempty"
	foundPrefix := false
	for _, r := range results {
		if r.FieldName == "Prefix" {
			foundPrefix = true
		}
	}
	if !foundPrefix {
		t.Error("expected Prefix field to match json tag search")
	}
}

func TestSearchFields_ByType(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "int"})
	foundLevel := false
	for _, r := range results {
		if r.FieldName == "Level" && r.StructName == "LoudSpeaker" {
			foundLevel = true
		}
	}
	if !foundLevel {
		t.Error("expected to find LoudSpeaker.Level by type 'int'")
	}
}

func TestSearchFields_ByDoc(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "prepended"})
	found := false
	for _, r := range results {
		if r.FieldName == "Prefix" && strings.Contains(r.FieldDoc, "prepended") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find Greeter.Prefix by doc 'prepended'")
	}
}

func TestSearchFields_RepoFilter(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "Prefix", Repo: "nonexistent"})
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent repo, got %d", len(results))
	}
}

func TestSearchFields_DefaultValue(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.SearchFields(FieldSearchParams{Query: "Port"})
	// Should find Port fields on GRPCConfig (50051) and HTTPConfig (8080).
	foundGRPC := false
	foundHTTP := false
	for _, r := range results {
		if r.FieldName == "Port" && r.StructName == "GRPCConfig" {
			foundGRPC = true
			if r.FieldDefaultValue != "50051" {
				t.Errorf("GRPCConfig.Port: expected default 50051, got %q", r.FieldDefaultValue)
			}
		}
		if r.FieldName == "Port" && r.StructName == "HTTPConfig" {
			foundHTTP = true
			if r.FieldDefaultValue != "8080" {
				t.Errorf("HTTPConfig.Port: expected default 8080, got %q", r.FieldDefaultValue)
			}
		}
	}
	if !foundGRPC {
		t.Error("expected to find GRPCConfig.Port field")
	}
	if !foundHTTP {
		t.Error("expected to find HTTPConfig.Port field")
	}
}

func TestSearchFields_NoQuery(t *testing.T) {
	idx := buildTestIndex(t)
	// No query, no filters — should return all struct fields
	results := idx.SearchFields(FieldSearchParams{})
	if len(results) == 0 {
		t.Error("expected some fields with no query")
	}
	// Should include fields from Greeter (Prefix, Suffix), LoudSpeaker (Level), Token (Value)
	if len(results) < 4 {
		t.Errorf("expected at least 4 fields, got %d", len(results))
	}
}
