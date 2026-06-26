package index

import (
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
