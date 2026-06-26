package index

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseGoMod reads a go.mod file and returns the module path.
func ParseGoMod(goModPath string) (string, error) {
	f, err := os.Open(goModPath)
	if err != nil {
		return "", fmt.Errorf("open go.mod: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	return "", fmt.Errorf("no module directive found in %s", goModPath)
}

// DeriveImportPath computes the import path for a Go file given its
// module path, repo root directory, and file path.
func DeriveImportPath(modulePath, repoRoot, filePath string) string {
	dir := filepath.Dir(filePath)
	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil || rel == "." {
		return modulePath
	}
	// Convert OS-specific separators to forward slashes for import paths.
	rel = filepath.ToSlash(rel)
	return modulePath + "/" + rel
}
