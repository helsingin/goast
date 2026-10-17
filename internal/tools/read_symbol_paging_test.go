package tools

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadSymbolLargeSource(t *testing.T) {
	root := t.TempDir()
	toolWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/paging\n\ngo 1.24\n")
	source := "package paging\n\nimport \"strings\"\n\n// Large preserves UTF-8 across source pages.\nfunc Large() string {\nreturn strings.TrimSpace(\"" + strings.Repeat("界", 14000) + "\")\n}\n"
	toolWriteFile(t, filepath.Join(root, "large.go"), source)
	session := newToolSession(t, newToolHolder(t, root))
	result, text := callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/paging", "name": "Large", "include_imports": true,
	})
	if result.IsError {
		t.Fatal(text)
	}
	if len(text) > 16384 {
		t.Fatalf("default read-symbol returned %d bytes; expected at most 16384", len(text))
	}
	_, full := callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/paging", "name": "Large", "include_imports": true, "max_bytes": 131072,
	})
	var rebuilt strings.Builder
	offset := 0
	digest := ""
	for pages := 0; ; pages++ {
		if pages > 100 || len(text) > 16384 || !utf8.ValidString(text) {
			t.Fatalf("invalid page %d: %d bytes", pages, len(text))
		}
		header, body, ok := strings.Cut(text, "\n\n")
		if !ok {
			t.Fatal("missing source page metadata")
		}
		var start, total int
		var next, actualDigest string
		if _, err := fmt.Sscanf(header, "// Source page: offset=%d next_offset=%s total_bytes=%d source_digest=%s", &start, &next, &total, &actualDigest); err != nil {
			t.Fatal(err)
		}
		if start != offset || total != len(full) || actualDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(full))) {
			t.Fatalf("incorrect source identity or range: %s", header)
		}
		digest = actualDigest
		rebuilt.WriteString(body)
		if next == "none" {
			break
		}
		if _, err := fmt.Sscan(next, &offset); err != nil || offset != rebuilt.Len() {
			t.Fatalf("invalid continuation: %s", next)
		}
		result, text = callWorktreeTool(t, session, "read-symbol", map[string]any{
			"package": "example.com/paging", "name": "Large", "include_imports": true,
			"offset": offset, "expected_digest": digest,
		})
		if result.IsError {
			t.Fatal(text)
		}
	}
	if rebuilt.String() != full || !strings.Contains(full, "import \"strings\"") || !strings.Contains(full, "// Large preserves") {
		t.Fatal("pages did not preserve the complete source, imports and doc comment")
	}
	toolWriteFile(t, filepath.Join(root, "large.go"), strings.Replace(source, "界", "海", 1))
	result, text = callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/paging", "name": "Large", "include_imports": true,
		"offset": offset, "expected_digest": digest,
	})
	if !result.IsError || !strings.Contains(text, "source changed") {
		t.Fatalf("source changed between pages without refusal: %s", text)
	}
}

func TestReadSymbolPageValidation(t *testing.T) {
	source := "界" + strings.Repeat("x", 40000)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	for _, args := range []ReadSymbolArgs{
		{Offset: -1}, {Offset: len(source) + 1}, {MaxBytes: -1},
		{MaxBytes: 1023}, {MaxBytes: 131073}, {Offset: 3},
		{Offset: 1, ExpectedDigest: digest}, {ExpectedDigest: "wrong"},
	} {
		if _, err := pageSymbolSource(source, args); err == nil {
			t.Errorf("accepted invalid arguments: %+v", args)
		}
	}
	for _, limit := range []int{1024, 16384, 131072} {
		page, err := pageSymbolSource(source, ReadSymbolArgs{MaxBytes: limit})
		if err != nil || len(page) > limit || !utf8.ValidString(page) {
			t.Errorf("limit %d: bytes=%d error=%v", limit, len(page), err)
		}
	}
	page, err := pageSymbolSource("small\nsource", ReadSymbolArgs{})
	if err != nil || page != "small\nsource" {
		t.Fatal("small source response changed")
	}
}
