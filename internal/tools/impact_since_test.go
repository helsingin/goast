package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestImpactSinceToolReturnsStructuredSourceBoundReport(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	runImpactToolGit(t, root, "init", "-q")
	runImpactToolGit(t, root, "config", "user.name", "Impact Tool Test")
	runImpactToolGit(t, root, "config", "user.email", "impact-tool@example.invalid")
	writeImpactToolFile(t, root, "go.mod", "module example.local/impact\n\ngo 1.24\n")
	writeImpactToolFile(t, root, "impact.go", "package impact\nfunc Run() bool { return false }\n")
	runImpactToolGit(t, root, "add", ".")
	runImpactToolGit(t, root, "commit", "-q", "-m", "base")
	base := runImpactToolGit(t, root, "rev-parse", "HEAD")
	writeImpactToolFile(t, root, "impact.go", "package impact\nfunc Run() bool { return true }\n")

	cfg := index.IndexConfig{Repos: []index.RepoConfig{{Name: "impact", Path: root}}}
	idx, err := index.BuildIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := index.NewHolder(idx, cfg)
	server := mcp.NewServer(&mcp.Implementation{Name: "impact-test", Version: "test"}, nil)
	RegisterImpactSince(server, holder)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "impact-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "impact-since", Arguments: map[string]any{"repository": "impact", "base_commit": base},
	})
	if err != nil || result.IsError {
		t.Fatalf("impact-since: result=%#v error=%v", result, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var report index.ImpactReport
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatalf("decode structured report: %v", err)
	}
	if report.Snapshot.Repository != "impact" || report.Snapshot.BaseCommit != base ||
		len(report.ChangedSymbols) != 1 || report.ChangedSymbols[0].Symbol.Name != "Run" ||
		report.ChangedSymbols[0].Change != index.SymbolModified || report.ImpactDigest == "" {
		t.Fatalf("report = %#v", report)
	}
}

func writeImpactToolFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runImpactToolGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
