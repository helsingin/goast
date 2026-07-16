package index

import (
	"go/build"
	"go/types"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindReferences_CrossPackageCall(t *testing.T) {
	idx := buildTestIndex(t)
	refs := idx.FindReferences("example.com/sample/pkg/greeter", "NewGreeter")
	if len(refs) == 0 {
		t.Fatal("expected references to NewGreeter from app.Run, got none")
	}
	found := false
	for _, r := range refs {
		s := idx.Symbols[r.FromSymbol]
		if s.Name == "Run" && s.ImportPath == "example.com/sample/pkg/app" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reference from app.Run; got %d refs", len(refs))
		for _, r := range refs {
			t.Logf("  from %s %s:%d", idx.Symbols[r.FromSymbol].Name, r.FilePath, r.Line)
		}
	}
}

func TestFindReferences_SamePackageCall(t *testing.T) {
	// app.helper calls app.Run — a same-package unqualified call that should resolve.
	idx := buildTestIndex(t)
	refs := idx.FindReferences("example.com/sample/pkg/app", "Run")
	if len(refs) == 0 {
		t.Fatal("expected references to app.Run from app.helper, got none")
	}
	for _, r := range refs {
		if idx.Symbols[r.FromSymbol].Name == "helper" {
			return
		}
	}
	t.Errorf("expected reference from app.helper; got:")
	for _, r := range refs {
		t.Logf("  from %s %s:%d", idx.Symbols[r.FromSymbol].Name, r.FilePath, r.Line)
	}
}

func TestFindReferences_CrossPackageMethodCall(t *testing.T) {
	idx := buildTestIndex(t)
	refs := idx.FindReferences("example.com/sample/pkg/greeter", "Greeter.Greet")
	assertReferenceFrom(t, idx, refs, "example.com/sample/pkg/app", "Run")
}

func TestFindReferences_MethodCallsFromInternalAndExternalTests(t *testing.T) {
	idx := buildTestIndexWithTests(t)
	refs := idx.FindReferences("example.com/sample/pkg/greeter", "Greeter.Greet")
	assertReferenceFrom(t, idx, refs, "example.com/sample/pkg/greeter", "callGreetFromInternalTest")
	assertReferenceFrom(t, idx, refs, "example.com/sample/pkg/greeter [greeter_test]", "callGreetFromExternalTest")
	assertReferenceCountFrom(t, idx, refs, "example.com/sample/pkg/greeter", "callGreetFromInternalTest", 2)

	resetRefs := idx.FindReferences("example.com/sample/pkg/greeter", "Greeter.Reset")
	assertReferenceFrom(t, idx, resetRefs, "example.com/sample/pkg/greeter", "TestInternalGreeterGreet")
}

func TestFindReferences_PromotedMethodResolvesDeclaration(t *testing.T) {
	idx := buildTestIndexWithTests(t)
	refs := idx.FindReferences("example.com/sample/pkg/greeter", "LoudSpeaker.Speak")
	assertReferenceFrom(t, idx, refs, "example.com/sample/pkg/greeter", "callPromotedMethodFromInternalTest")
	if refs := idx.FindReferences("example.com/sample/pkg/greeter", "HappySpeaker.Speak"); refs != nil {
		t.Fatalf("promoted method must resolve to its LoudSpeaker declaration, got HappySpeaker refs: %v", refs)
	}
}

func TestFindReferences_InterfaceMethodCall(t *testing.T) {
	idx := buildTestIndexWithTests(t)
	refs := idx.FindReferences("example.com/sample/pkg/greeter", "Speaker.Speak")
	assertReferenceFrom(t, idx, refs, "example.com/sample/pkg/greeter", "callInterfaceMethodFromInternalTest")
}

func TestFindReferences_CoherentBuildContextsUnionAndDeduplicate(t *testing.T) {
	fixture := filepath.Join(filepath.Dir(testdataDir()), "build_variants")
	cgoDisabled := false
	cfg := IndexConfig{
		Repos: []RepoConfig{{Path: fixture, TypedMethodReferences: true}},
		BuildContexts: []BuildContext{
			{GOOS: "linux", GOARCH: "amd64", CGOEnabled: &cgoDisabled},
			{GOOS: "darwin", GOARCH: "arm64", CGOEnabled: &cgoDisabled},
			{GOOS: "js", GOARCH: "wasm", CGOEnabled: &cgoDisabled},
			{GOOS: "linux", GOARCH: "amd64", CGOEnabled: &cgoDisabled}, // duplicate context
		},
	}
	idx, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	refs := idx.FindReferences("example.com/buildvariants/worker", "Runner.Run")
	for _, caller := range []string{"CommonCall", "LinuxCall", "DarwinCall", "WasmCall", "NoCgoCall"} {
		assertReferenceCountFrom(t, idx, refs, "example.com/buildvariants/worker", caller, 1)
	}
	assertReferenceCountFrom(t, idx, refs, "example.com/buildvariants/worker", "CgoCall", 0)
	variantCallers := 0
	for _, ref := range refs {
		caller := idx.Symbols[ref.FromSymbol]
		if caller.Receiver == "Runner" && caller.Name == "PlatformCaller" {
			variantCallers++
			if caller.FilePath != ref.FilePath {
				t.Errorf("variant caller resolved to wrong declaration: caller %s, reference %s", caller.FilePath, ref.FilePath)
			}
		}
	}
	if variantCallers != 2 {
		t.Fatalf("platform caller references: got %d, want 2", variantCallers)
	}
}

func TestFindReferences_TypedMethodAnalysisIsOptIn(t *testing.T) {
	cfg := IndexConfig{Repos: []RepoConfig{{Path: testdataDir()}}}
	idx, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if refs := idx.FindReferences("example.com/sample/pkg/greeter", "Greeter.Greet"); refs != nil {
		t.Fatalf("typed method references must be disabled by default, got %v", refs)
	}
	if refs := idx.FindReferences("example.com/sample/pkg/greeter", "NewGreeter"); len(refs) == 0 {
		t.Fatal("syntax-derived references must remain enabled")
	}
}

func TestTypedReferenceBuildContextsNormalizeAndValidate(t *testing.T) {
	cgoDisabled := false
	contexts, err := effectiveBuildContexts([]BuildContext{
		{GOOS: "js", GOARCH: "wasm", CGOEnabled: &cgoDisabled, BuildTags: []string{"integration", "race", "integration"}},
		{GOOS: "js", GOARCH: "wasm", CGOEnabled: &cgoDisabled, BuildTags: []string{"race", "integration"}},
	})
	if err != nil {
		t.Fatalf("effectiveBuildContexts: %v", err)
	}
	if len(contexts) != 1 {
		t.Fatalf("semantically duplicate contexts were not deduplicated: %d", len(contexts))
	}
	if contexts[0].CgoEnabled {
		t.Error("explicit cgo-disabled context was changed")
	}
	if got := strings.Join(contexts[0].BuildTags, ","); got != "integration,race" {
		t.Fatalf("normalized build tags: got %q", got)
	}

	fixture := filepath.Join(filepath.Dir(testdataDir()), "build_variants")
	for _, bad := range []BuildContext{
		{GOOS: "not-a-goos", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "not-a-goarch"},
		{GOOS: "linux", GOARCH: "amd64", BuildTags: []string{"bad,tag"}},
	} {
		_, err := BuildIndex(IndexConfig{
			Repos:         []RepoConfig{{Path: fixture, TypedMethodReferences: true}},
			BuildContexts: []BuildContext{bad},
		})
		if err == nil {
			t.Fatalf("invalid build context was accepted: %+v", bad)
		}
	}
}

func TestTypedReferenceBuildContextsUseTargetSizesAndPreserveSameArchToolTags(t *testing.T) {
	cgoDisabled := false
	contexts, err := effectiveBuildContexts([]BuildContext{{
		GOOS:       "linux",
		GOARCH:     "386",
		CGOEnabled: &cgoDisabled,
	}})
	if err != nil {
		t.Fatalf("386 build context: %v", err)
	}
	sizes := types.SizesFor(contexts[0].Compiler, contexts[0].GOARCH)
	if sizes == nil || sizes.Sizeof(types.Typ[types.Uintptr]) != 4 {
		t.Fatalf("386 uintptr size: got %v", sizes)
	}

	alternateOS := "linux"
	if build.Default.GOOS == alternateOS {
		alternateOS = "windows"
	}
	sameArch, err := effectiveBuildContexts([]BuildContext{{
		GOOS:   alternateOS,
		GOARCH: build.Default.GOARCH,
	}})
	if err != nil {
		t.Fatalf("same-architecture alternate OS context: %v", err)
	}
	if strings.Join(sameArch[0].ToolTags, "\x00") != strings.Join(build.Default.ToolTags, "\x00") {
		t.Fatalf("same-architecture tool tags changed: got %v, want %v", sameArch[0].ToolTags, build.Default.ToolTags)
	}
}

func TestFindReferences_DeterministicAndDeduplicated(t *testing.T) {
	first := buildTestIndexWithTests(t)
	second := buildTestIndexWithTests(t)
	keyPath, keyName := "example.com/sample/pkg/greeter", "Greeter.Greet"
	firstRefs := first.FindReferences(keyPath, keyName)
	secondRefs := second.FindReferences(keyPath, keyName)
	if len(firstRefs) != len(secondRefs) {
		t.Fatalf("reference count changed across builds: %d != %d", len(firstRefs), len(secondRefs))
	}
	seen := make(map[Reference]bool, len(firstRefs))
	for i := range firstRefs {
		if firstRefs[i] != secondRefs[i] {
			t.Fatalf("reference %d changed across builds: %+v != %+v", i, firstRefs[i], secondRefs[i])
		}
		if seen[firstRefs[i]] {
			t.Fatalf("duplicate reference: %+v", firstRefs[i])
		}
		seen[firstRefs[i]] = true
	}
}

func TestFindReferences_SkipsBuiltinsAndExternal(t *testing.T) {
	// Sprintf lives in stdlib fmt — not indexed. Must not appear as a ref target.
	idx := buildTestIndex(t)
	for key := range idx.References {
		if strings.HasSuffix(key, "\x00Sprintf") {
			t.Errorf("stdlib Sprintf should not appear in references map, key=%q", key)
		}
		// Builtins like `make`, `len` should never land here either.
		if strings.HasSuffix(key, "\x00len") || strings.HasSuffix(key, "\x00make") {
			t.Errorf("builtin %q should not appear in references map", key)
		}
	}
}

func TestFindReferences_Unknown(t *testing.T) {
	idx := buildTestIndex(t)
	if refs := idx.FindReferences("example.com/sample/pkg/greeter", "DoesNotExist"); refs != nil {
		t.Errorf("expected nil for unknown symbol, got %v", refs)
	}
}

func assertReferenceFrom(t *testing.T, idx *Index, refs []Reference, importPath, name string) {
	t.Helper()
	for _, ref := range refs {
		from := idx.Symbols[ref.FromSymbol]
		if from.ImportPath == importPath && from.Name == name {
			return
		}
	}
	t.Fatalf("missing reference from %s.%s in %+v", importPath, name, refs)
}

func assertReferenceCountFrom(t *testing.T, idx *Index, refs []Reference, importPath, name string, want int) {
	t.Helper()
	count := 0
	columns := make(map[int]bool)
	for _, ref := range refs {
		from := idx.Symbols[ref.FromSymbol]
		if from.ImportPath == importPath && from.Name == name {
			count++
			columns[ref.Column] = true
		}
	}
	if count != want {
		t.Fatalf("references from %s.%s: got %d, want %d", importPath, name, count, want)
	}
	if len(columns) != want {
		t.Fatalf("same-line references from %s.%s lost column identity: %v", importPath, name, columns)
	}
}
