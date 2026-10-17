package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReadSymbolArgs are the input arguments for the read-symbol tool.
type ReadSymbolArgs struct {
	Package        string `json:"package" jsonschema:"required,Full import path of the package (e.g. github.com/acme/platform/auth)"`
	Name           string `json:"name" jsonschema:"required,Symbol name. For methods use Type.Method format (e.g. KeyManager.GetKey)"`
	IncludeImports bool   `json:"include_imports,omitempty" jsonschema:"Also include the file's import block"`
	Offset         int    `json:"offset,omitempty" jsonschema:"Byte offset from a previous next_offset; defaults to 0. Keep package, name and include_imports unchanged when continuing."`
	MaxBytes       int    `json:"max_bytes,omitempty" jsonschema:"Maximum UTF-8 bytes per reply including pagination metadata; defaults to 16384, range 1024 through 131072."`
	ExpectedDigest string `json:"expected_digest,omitempty" jsonschema:"Source SHA-256 from the previous page; required when offset is greater than 0. Refuses source changes between pages."`
}

// RegisterReadSymbol registers the read-symbol tool on the server.
func RegisterReadSymbol(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read-symbol",
		Description: "Read source code of a specific Go symbol. Replies are bounded to 16 KiB by default. Large symbols return next_offset and source_digest: continue with offset and expected_digest until next_offset=none before treating the source as complete. Use search-symbols first to find the package and name. After code changes, call reindex first so line numbers are accurate.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ReadSymbolArgs) (*mcp.CallToolResult, any, error) {
		sym := holder.Get().GetSymbol(args.Package, args.Name)
		if sym == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Symbol %q not found in package %q. Use search-symbols to find the correct package and name.", args.Name, args.Package)},
				},
				IsError: true,
			}, nil, nil
		}

		source, err := readSourceLines(sym.FilePath, sym.Line, sym.EndLine)
		if err != nil {
			return nil, nil, fmt.Errorf("reading source: %w", err)
		}

		// Scan backwards from the symbol start for doc comments.
		docLines, err := readDocComment(sym.FilePath, sym.Line)
		if err != nil {
			// Non-fatal — just skip doc comments.
			docLines = ""
		}

		var b strings.Builder
		fmt.Fprintf(&b, "// File: %s (lines %d-%d)\n", sym.FilePath, sym.Line, sym.EndLine)
		fmt.Fprintf(&b, "// Package: %s | Import: %s | Repo: %s\n\n", sym.PkgName, sym.ImportPath, sym.Repo)

		if args.IncludeImports {
			imports, err := readImportBlock(sym.FilePath)
			if err == nil && imports != "" {
				b.WriteString(imports)
				b.WriteString("\n\n")
			}
		}

		if docLines != "" {
			b.WriteString(docLines)
			b.WriteString("\n")
		}
		b.WriteString(source)

		text, err := pageSymbolSource(b.String(), args)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})
}

// pageSymbolSource pages the existing rendered source without omitting imports,
// comments or code. Offsets address that exact UTF-8 text, not the page headers.
func pageSymbolSource(source string, args ReadSymbolArgs) (string, error) {
	limit := args.MaxBytes
	if limit == 0 {
		limit = 16384
	}
	if limit < 1024 || limit > 131072 {
		return "", fmt.Errorf("max_bytes must be between 1024 and 131072")
	}
	if args.Offset < 0 || args.Offset > len(source) {
		return "", fmt.Errorf("offset is outside the source; start again at offset 0")
	}
	if !utf8.ValidString(source) || (args.Offset < len(source) && !utf8.RuneStart(source[args.Offset])) {
		return "", fmt.Errorf("source or offset is not valid UTF-8")
	}
	if args.Offset > 0 && args.ExpectedDigest == "" {
		return "", fmt.Errorf("expected_digest is required when continuing source pages")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	if args.ExpectedDigest != "" && args.ExpectedDigest != digest {
		return "", fmt.Errorf("source changed between pages; reindex and start again at offset 0 without expected_digest")
	}
	// Preserve the original small-symbol response exactly.
	if args.Offset == 0 && len(source) <= limit {
		return source, nil
	}
	// Reserve ample room for fixed metadata and 64-bit offsets. This bound
	// includes the header, not just the source body.
	end := args.Offset + min(limit-512, len(source)-args.Offset)
	if end < len(source) {
		for end > args.Offset && !utf8.RuneStart(source[end]) {
			end--
		}
		if newline := strings.LastIndexByte(source[args.Offset:end], '\n'); newline >= 0 {
			end = args.Offset + newline + 1
		}
	}
	next := "none"
	continuation := "// Complete: no more source pages.\n"
	if end < len(source) {
		next = fmt.Sprint(end)
		continuation = "// Continue read-symbol with offset=next_offset and expected_digest=source_digest; keep package, name and include_imports unchanged.\n"
	}
	header := fmt.Sprintf("// Source page: offset=%d next_offset=%s total_bytes=%d source_digest=%s\n%s\n", args.Offset, next, len(source), digest, continuation)
	return header + source[args.Offset:end], nil
}

// readSourceLines reads lines [start, end] (1-indexed, inclusive) from a file.
func readSourceLines(filePath string, start, end int) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= start && lineNum <= end {
			lines = append(lines, scanner.Text())
		}
		if lineNum > end {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// readDocComment scans backwards from symbolLine to find contiguous // comment lines.
func readDocComment(filePath string, symbolLine int) (string, error) {
	if symbolLine <= 1 {
		return "", nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	// Read all lines up to the symbol line.
	var allLines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= symbolLine {
			break
		}
		allLines = append(allLines, scanner.Text())
	}

	// Scan backwards from symbolLine-1 to find comment block.
	var docLines []string
	for i := len(allLines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(allLines[i])
		if strings.HasPrefix(line, "//") {
			docLines = append([]string{allLines[i]}, docLines...)
		} else if line == "" {
			// Allow one blank line between comment and symbol, but stop on non-comment.
			if i > 0 && strings.HasPrefix(strings.TrimSpace(allLines[i-1]), "//") {
				continue
			}
			break
		} else {
			break
		}
	}

	return strings.Join(docLines, "\n"), nil
}

// readImportBlock reads the import(...) block from a Go file.
func readImportBlock(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	inImport := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "import (" {
			inImport = true
			lines = append(lines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "import \"") || strings.HasPrefix(trimmed, "import `") {
			lines = append(lines, line)
			break
		}
		if inImport {
			lines = append(lines, line)
			if trimmed == ")" {
				break
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
