package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReadSymbolArgs are the input arguments for the read-symbol tool.
type ReadSymbolArgs struct {
	Package        string `json:"package" jsonschema:"required,Full import path of the package (e.g. github.com/acme/platform/auth)"`
	Name           string `json:"name" jsonschema:"required,Symbol name. For methods use Type.Method format (e.g. KeyManager.GetKey)"`
	IncludeImports bool   `json:"include_imports,omitempty" jsonschema:"Also include the file's import block"`
}

// RegisterReadSymbol registers the read-symbol tool on the server.
func RegisterReadSymbol(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read-symbol",
		Description: "Read the full source code of a specific Go symbol. Use search-symbols first to find the symbol's package and name, then read-symbol to see its implementation. If you've made code changes recently, call reindex first so line numbers are accurate.",
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

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: b.String()},
			},
		}, nil, nil
	})
}

// readSourceLines reads lines [start, end] (1-indexed, inclusive) from a file.
func readSourceLines(filePath string, start, end int) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

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
	defer f.Close()

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
	defer f.Close()

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
