package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "sample_repo")
}

func buildTestIndex(t *testing.T) *Index {
	t.Helper()
	cfg := IndexConfig{
		Repos: []RepoConfig{{Path: testdataDir(), TypedMethodReferences: true}},
	}
	idx, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return idx
}

func buildTestIndexWithTests(t *testing.T) *Index {
	t.Helper()
	cfg := IndexConfig{
		Repos: []RepoConfig{{Path: testdataDir(), IncludeTests: true, TypedMethodReferences: true}},
	}
	idx, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex with tests: %v", err)
	}
	return idx
}

func TestBuildIndex_SymbolCount(t *testing.T) {
	idx := buildTestIndex(t)
	if len(idx.Symbols) == 0 {
		t.Fatal("expected symbols, got 0")
	}
	// Count expected symbols from testdata:
	// greeter.go: DefaultGreeting(const), GreetCount(var), Greeter(type), Greet(method), Reset(method),
	//             Speaker(type), LoudSpeaker(type), LoudSpeaker.Speak(method), LoudSpeaker.Volume(method),
	//             GRPCConfig(type), HTTPConfig(type), GreeterConfig(type), DefaultGreeterConfig(func),
	//             HappySpeaker(type), AudioDevice(type), MuteLoudSpeaker(type), MuteLoudSpeaker.Mute(method),
	//             NewGreeter(func), FormatGreeting(func) = 19
	// helpers.go: sanitizeName(func) = 1
	// secret.go: Token(type), Validate(method) = 2
	// app.go: Run(func), helper(func) = 2
	// generated.pb.go: MessageType(type), MessageType_UNKNOWN(const), MessageType_REQUEST(const),
	//                   ProtoMessage(type), GetId(method), GetType(method) = 6
	// generated_grpc.pb.go: GreeterServiceServer(type), UnsafeGreeterServiceServer(type) = 2
	// real_test/real.go: RealPackageSymbol(const) = 1
	// Total: 33
	if len(idx.Symbols) != 33 {
		t.Errorf("expected 33 symbols, got %d", len(idx.Symbols))
		for _, s := range idx.Symbols {
			t.Logf("  %s %s.%s", s.Kind, s.PkgName, s.Name)
		}
	}
}

func TestBuildIndex_SkipsUnavailableRepositories(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	notDirectory := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("not a repository"), 0o600); err != nil {
		t.Fatalf("create non-directory repository path: %v", err)
	}

	idx, err := BuildIndex(IndexConfig{Repos: []RepoConfig{
		{Name: "missing", Path: missing},
		{Name: "file", Path: notDirectory},
		{Name: "sample", Path: testdataDir()},
	}})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if got, want := len(idx.Symbols), 33; got != want {
		t.Fatalf("symbol count: got %d, want %d", got, want)
	}
	for _, symbol := range idx.Symbols {
		if symbol.Repo == "missing" || symbol.Repo == "file" {
			t.Fatalf("indexed unavailable repository %q: %+v", symbol.Repo, symbol)
		}
	}
}

func TestBuildIndex_TestsDisabledByDefault(t *testing.T) {
	idx := buildTestIndex(t)
	for _, symbol := range idx.Symbols {
		if strings.HasSuffix(symbol.FilePath, "_test.go") {
			t.Fatalf("default index unexpectedly contains test symbol %s from %s", symbol.Name, symbol.FilePath)
		}
	}
}

func TestBuildIndex_IncludeInternalAndExternalTests(t *testing.T) {
	idx := buildTestIndexWithTests(t)
	if got, want := len(idx.Symbols), 39; got != want {
		t.Fatalf("symbol count with tests: got %d, want %d", got, want)
	}

	wantPaths := map[string]string{
		"TestInternalGreeterGreet": "example.com/sample/pkg/greeter",
		"TestExternalGreeterGreet": "example.com/sample/pkg/greeter [greeter_test]",
		"RealPackageSymbol":        "example.com/sample/pkg/greeter_test",
	}
	for name, wantPath := range wantPaths {
		found := false
		for _, symbol := range idx.Symbols {
			if symbol.Name != name {
				continue
			}
			found = true
			if symbol.ImportPath != wantPath {
				t.Errorf("%s import path: got %q, want %q", name, symbol.ImportPath, wantPath)
			}
		}
		if !found {
			t.Errorf("missing indexed test symbol %s", name)
		}
	}

	foundExternalPackage := false
	for _, pkg := range idx.Packages {
		if pkg.ImportPath == "example.com/sample/pkg/greeter [greeter_test]" {
			foundExternalPackage = true
			if pkg.Name != "greeter_test" || pkg.FileCount != 1 || pkg.SymbolCount != 2 {
				t.Errorf("external test package metadata: %+v", pkg)
			}
		}
	}
	if !foundExternalPackage {
		t.Fatal("external test package was not indexed separately")
	}
}

func TestBuildIndex_ExplicitTestExclusionWins(t *testing.T) {
	cfg := IndexConfig{
		Repos:           []RepoConfig{{Path: testdataDir(), IncludeTests: true}},
		ExcludePatterns: []string{"**/*_test.go"},
	}
	idx, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if got, want := len(idx.Symbols), 33; got != want {
		t.Fatalf("symbol count with explicit test exclusion: got %d, want %d", got, want)
	}
}

func TestBuildIndex_Packages(t *testing.T) {
	idx := buildTestIndex(t)
	// Expected packages: greeter, greeter_test (a real production package),
	// secret, app, sample (root — from generated.pb.go).
	if len(idx.Packages) != 5 {
		t.Errorf("expected 5 packages, got %d", len(idx.Packages))
		for _, p := range idx.Packages {
			t.Logf("  %s (%s)", p.ImportPath, p.Name)
		}
	}
}

func TestBuildIndex_FuncSignature(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "NewGreeter" {
			if s.Kind != SymbolFunc {
				t.Errorf("NewGreeter: expected kind=func, got %s", s.Kind)
			}
			if s.Signature == "" {
				t.Error("NewGreeter: expected non-empty signature")
			}
			if s.Exported != true {
				t.Error("NewGreeter: expected exported=true")
			}
			return
		}
	}
	t.Error("symbol NewGreeter not found")
}

func TestBuildIndex_MethodReceiver(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "Greet" {
			if s.Kind != SymbolMethod {
				t.Errorf("Greet: expected kind=method, got %s", s.Kind)
			}
			if s.Receiver != "Greeter" {
				t.Errorf("Greet: expected receiver=Greeter, got %s", s.Receiver)
			}
			return
		}
	}
	t.Error("symbol Greet not found")
}

func TestBuildIndex_StructFields(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "Greeter" && s.Kind == SymbolType {
			if s.TypeKind != TypeStruct {
				t.Errorf("Greeter: expected type_kind=struct, got %s", s.TypeKind)
			}
			if len(s.Fields) != 2 {
				t.Errorf("Greeter: expected 2 fields, got %d", len(s.Fields))
			}
			for _, f := range s.Fields {
				if f.Name == "Prefix" {
					if f.Tag != "`json:\"prefix\"`" {
						t.Errorf("Prefix field: expected tag `json:\"prefix\"`, got %s", f.Tag)
					}
				}
			}
			return
		}
	}
	t.Error("symbol Greeter (type) not found")
}

func TestBuildIndex_InterfaceMethods(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "Speaker" {
			if s.TypeKind != TypeInterface {
				t.Errorf("Speaker: expected type_kind=interface, got %s", s.TypeKind)
			}
			if len(s.Methods) != 2 {
				t.Errorf("Speaker: expected 2 methods, got %d: %v", len(s.Methods), s.Methods)
			}
			return
		}
	}
	t.Error("symbol Speaker not found")
}

func TestBuildIndex_DocExtraction(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "FormatGreeting" {
			if s.Doc == "" {
				t.Error("FormatGreeting: expected non-empty doc")
			}
			if s.DocSummary == "" {
				t.Error("FormatGreeting: expected non-empty doc summary")
			}
			return
		}
	}
	t.Error("symbol FormatGreeting not found")
}

func TestBuildIndex_UnexportedFunc(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "sanitizeName" {
			if s.Exported {
				t.Error("sanitizeName: expected exported=false")
			}
			if s.Kind != SymbolFunc {
				t.Errorf("sanitizeName: expected kind=func, got %s", s.Kind)
			}
			return
		}
	}
	t.Error("symbol sanitizeName not found")
}

func TestBuildIndex_GeneratedFlag(t *testing.T) {
	idx := buildTestIndex(t)
	found := false
	for _, s := range idx.Symbols {
		if s.Name == "ProtoMessage" {
			found = true
			if !s.Generated {
				t.Error("ProtoMessage: expected generated=true")
			}
		}
	}
	if !found {
		t.Error("symbol ProtoMessage not found")
	}
}

func TestBuildIndex_InternalPackage(t *testing.T) {
	idx := buildTestIndex(t)
	for _, p := range idx.Packages {
		if p.Name == "secret" {
			if !p.Internal {
				t.Error("secret package: expected internal=true")
			}
			return
		}
	}
	t.Error("package secret not found")
}

func TestBuildIndex_ImportPaths(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "NewGreeter" {
			expected := "example.com/sample/pkg/greeter"
			if s.ImportPath != expected {
				t.Errorf("NewGreeter: expected import path %q, got %q", expected, s.ImportPath)
			}
			return
		}
	}
	t.Error("symbol NewGreeter not found")
}

func TestBuildIndex_PackageDoc(t *testing.T) {
	idx := buildTestIndex(t)
	for _, p := range idx.Packages {
		if p.Name == "greeter" {
			if p.Doc == "" {
				t.Error("greeter package: expected non-empty doc")
			}
			if p.DocSummary == "" {
				t.Error("greeter package: expected non-empty doc summary")
			}
			return
		}
	}
	t.Error("package greeter not found")
}

func TestBuildIndex_ConstAndVar(t *testing.T) {
	idx := buildTestIndex(t)
	foundConst := false
	foundVar := false
	for _, s := range idx.Symbols {
		if s.Name == "DefaultGreeting" && s.Kind == SymbolConst {
			foundConst = true
		}
		if s.Name == "GreetCount" && s.Kind == SymbolVar {
			foundVar = true
		}
	}
	if !foundConst {
		t.Error("const DefaultGreeting not found")
	}
	if !foundVar {
		t.Error("var GreetCount not found")
	}
}

func TestConstValueInSignature(t *testing.T) {
	idx := buildTestIndex(t)
	found := false
	for _, s := range idx.Symbols {
		if s.Name == "DefaultGreeting" && s.Kind == SymbolConst {
			found = true
			want := `DefaultGreeting = "Hello"`
			if s.Signature != want {
				t.Errorf("DefaultGreeting signature: want %q, got %q", want, s.Signature)
			}
		}
	}
	if !found {
		t.Error("const DefaultGreeting not found")
	}
}

func TestMethodDescriptor(t *testing.T) {
	idx := buildTestIndex(t)

	// Check that LoudSpeaker.Speak has a descriptor.
	for _, s := range idx.Symbols {
		if s.Name == "Speak" && s.Receiver == "LoudSpeaker" {
			expected := "Speak(string)(error)"
			if s.MethodDescriptor != expected {
				t.Errorf("LoudSpeaker.Speak: expected descriptor %q, got %q", expected, s.MethodDescriptor)
			}
			return
		}
	}
	t.Error("LoudSpeaker.Speak not found")
}

func TestMethodDescriptor_Volume(t *testing.T) {
	idx := buildTestIndex(t)

	for _, s := range idx.Symbols {
		if s.Name == "Volume" && s.Receiver == "LoudSpeaker" {
			expected := "Volume()(int)"
			if s.MethodDescriptor != expected {
				t.Errorf("LoudSpeaker.Volume: expected descriptor %q, got %q", expected, s.MethodDescriptor)
			}
			return
		}
	}
	t.Error("LoudSpeaker.Volume not found")
}

func TestInterfaceMethodDescriptors(t *testing.T) {
	idx := buildTestIndex(t)

	for _, s := range idx.Symbols {
		if s.Name == "Speaker" && s.TypeKind == TypeInterface {
			if len(s.MethodDescriptors) != 2 {
				t.Fatalf("Speaker: expected 2 method descriptors, got %d: %v", len(s.MethodDescriptors), s.MethodDescriptors)
			}
			// Should contain Speak(string)(error) and Volume()(int)
			descs := make(map[string]bool)
			for _, d := range s.MethodDescriptors {
				descs[d] = true
			}
			if !descs["Speak(string)(error)"] {
				t.Errorf("Speaker: missing descriptor Speak(string)(error), got %v", s.MethodDescriptors)
			}
			if !descs["Volume()(int)"] {
				t.Errorf("Speaker: missing descriptor Volume()(int), got %v", s.MethodDescriptors)
			}
			return
		}
	}
	t.Error("Speaker interface not found")
}

func TestBuildImplMap_SpeakerHasLoudSpeaker(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.FindImplementations("example.com/sample/pkg/greeter", "Speaker")
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
	}
	// LoudSpeaker implements directly; HappySpeaker and MuteLoudSpeaker via
	// embedded-struct method promotion.
	for _, want := range []string{"LoudSpeaker", "HappySpeaker", "MuteLoudSpeaker"} {
		if !names[want] {
			t.Errorf("expected %s among Speaker implementations, got %v", want, names)
		}
	}
}

func TestBuildImplMap_PromotedMethodsViaEmbedding(t *testing.T) {
	idx := buildTestIndex(t)
	// HappySpeaker has no methods of its own — it satisfies Speaker purely
	// via the embedded LoudSpeaker. Without method-set flattening this fails.
	ifaces := idx.FindInterfaces("example.com/sample/pkg/greeter", "HappySpeaker")
	found := false
	for _, r := range ifaces {
		if r.Name == "Speaker" {
			found = true
		}
	}
	if !found {
		t.Errorf("HappySpeaker should satisfy Speaker via embedded LoudSpeaker; got interfaces: %v", ifaces)
	}
}

func TestBuildImplMap_EmbeddedInterfaceFlattening(t *testing.T) {
	idx := buildTestIndex(t)
	// AudioDevice embeds Speaker and adds Mute. MuteLoudSpeaker embeds
	// LoudSpeaker (giving Speak+Volume) and declares Mute — so it must
	// satisfy AudioDevice, which requires interface-embed flattening on
	// the AudioDevice side AND struct-embed flattening on the MuteLoudSpeaker side.
	impls := idx.FindImplementations("example.com/sample/pkg/greeter", "AudioDevice")
	found := false
	for _, r := range impls {
		if r.Name == "MuteLoudSpeaker" {
			found = true
		}
	}
	if !found {
		t.Errorf("MuteLoudSpeaker should implement AudioDevice; got impls: %v", impls)
	}
}

func TestBuildImplMap_LoudSpeakerSatisfiesSpeaker(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.FindInterfaces("example.com/sample/pkg/greeter", "LoudSpeaker")
	if len(results) != 1 {
		t.Fatalf("expected 1 interface for LoudSpeaker, got %d", len(results))
	}
	if results[0].Name != "Speaker" {
		t.Errorf("expected Speaker, got %s", results[0].Name)
	}
}

func TestBuildImplMap_GreeterDoesNotImplementSpeaker(t *testing.T) {
	idx := buildTestIndex(t)
	results := idx.FindInterfaces("example.com/sample/pkg/greeter", "Greeter")
	// Greeter has Greet and Reset, not Speak and Volume — should not satisfy Speaker
	for _, r := range results {
		if r.Name == "Speaker" {
			t.Error("Greeter should NOT implement Speaker")
		}
	}
}

func TestExtractDefaults_GreeterConfig(t *testing.T) {
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "GreeterConfig" && s.Kind == SymbolType {
			if len(s.Fields) != 3 {
				t.Fatalf("GreeterConfig: expected 3 fields, got %d", len(s.Fields))
			}
			expected := map[string]string{
				"GRPC":    "GRPCConfig{...}",
				"HTTP":    "HTTPConfig{...}",
				"Verbose": "false",
			}
			for _, f := range s.Fields {
				want, ok := expected[f.Name]
				if !ok {
					continue
				}
				if f.DefaultValue != want {
					t.Errorf("GreeterConfig.%s: expected default %q, got %q", f.Name, want, f.DefaultValue)
				}
			}
			return
		}
	}
	t.Error("GreeterConfig type not found")
}

func TestExtractDefaults_NestedStruct(t *testing.T) {
	// GRPCConfig and HTTPConfig get defaults from the nested composite literals
	// in DefaultGreeterConfig().
	idx := buildTestIndex(t)

	// Check GRPCConfig fields got defaults from the nested literal.
	for _, s := range idx.Symbols {
		if s.Name == "GRPCConfig" && s.Kind == SymbolType {
			expected := map[string]string{
				"Host": `"127.0.0.1"`,
				"Port": "50051",
			}
			for _, f := range s.Fields {
				want, ok := expected[f.Name]
				if !ok {
					continue
				}
				if f.DefaultValue != want {
					t.Errorf("GRPCConfig.%s: expected default %q, got %q", f.Name, want, f.DefaultValue)
				}
			}
			break
		}
	}

	// Check HTTPConfig fields got defaults.
	for _, s := range idx.Symbols {
		if s.Name == "HTTPConfig" && s.Kind == SymbolType {
			expected := map[string]string{
				"Port":          "8080",
				"ReadTimeoutMS": "5000",
			}
			for _, f := range s.Fields {
				want, ok := expected[f.Name]
				if !ok {
					continue
				}
				if f.DefaultValue != want {
					t.Errorf("HTTPConfig.%s: expected default %q, got %q", f.Name, want, f.DefaultValue)
				}
			}
			break
		}
	}
}

func TestExtractDefaults_ParameterRef(t *testing.T) {
	// NewGreeter returns &Greeter{Prefix: prefix} where prefix is a function parameter.
	// The identifier "prefix" should be extracted as-is.
	idx := buildTestIndex(t)
	for _, s := range idx.Symbols {
		if s.Name == "Greeter" && s.Kind == SymbolType {
			for _, f := range s.Fields {
				if f.Name == "Prefix" {
					if f.DefaultValue != "prefix" {
						t.Errorf("Greeter.Prefix: expected default %q (param ref), got %q", "prefix", f.DefaultValue)
					}
					return
				}
			}
			t.Error("Greeter.Prefix field not found")
			return
		}
	}
	t.Error("Greeter type not found")
}

func TestParseGoMod(t *testing.T) {
	path := filepath.Join(testdataDir(), "go.mod")
	mod, err := ParseGoMod(path)
	if err != nil {
		t.Fatalf("ParseGoMod: %v", err)
	}
	if mod != "example.com/sample" {
		t.Errorf("expected module example.com/sample, got %s", mod)
	}
}

func TestDeriveImportPath(t *testing.T) {
	tests := []struct {
		modulePath string
		repoRoot   string
		filePath   string
		want       string
	}{
		{"example.com/sample", "/repo", "/repo/main.go", "example.com/sample"},
		{"example.com/sample", "/repo", "/repo/pkg/foo/bar.go", "example.com/sample/pkg/foo"},
		{"example.com/sample", "/repo", "/repo/internal/x/y.go", "example.com/sample/internal/x"},
	}
	for _, tt := range tests {
		got := DeriveImportPath(tt.modulePath, tt.repoRoot, tt.filePath)
		if got != tt.want {
			t.Errorf("DeriveImportPath(%q, %q, %q) = %q, want %q",
				tt.modulePath, tt.repoRoot, tt.filePath, got, tt.want)
		}
	}
}

func TestMatchPathGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*_test.go", "pkg/greeter/greeter_test.go", true},
		{"**/*_test.go", "greeter_test.go", true},
		{"vendor/**", "vendor/example.com/lib/x.go", true},
		{"testdata/**", "testdata/sample/pkg/file.go", true},
		{"pkg/*/generated?.go", "pkg/api/generated1.go", true},
		{"pkg/*/generated?.go", "pkg/api/nested/generated1.go", false},
		{"**/*.go", "README.md", false},
	}
	for _, tt := range tests {
		if got := matchPathGlob(tt.pattern, tt.name); got != tt.want {
			t.Errorf("matchPathGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"Hello world.", "Hello world."},
		{"First sentence. Second sentence.", "First sentence."},
		{"First sentence.\nSecond paragraph.", "First sentence."},
		{"No period here", "No period here"},
		{"Multi\nline\ntext. More.", "Multi line text."},
	}
	for _, tt := range tests {
		got := FirstSentence(tt.input)
		if got != tt.want {
			t.Errorf("FirstSentence(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
