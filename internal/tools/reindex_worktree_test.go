package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolWorktreeFixture struct {
	main    string
	feature string
	head    string
}

func newToolWorktreeFixture(t *testing.T) toolWorktreeFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for worktree tests")
	}
	root := filepath.Join(t.TempDir(), "fixture with spaces")
	mainRoot := filepath.Join(root, "main")
	featureRoot := filepath.Join(root, "feature worktree")
	if err := os.MkdirAll(mainRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	toolGit(t, mainRoot, "init")
	toolGit(t, mainRoot, "config", "user.name", "GoAST Test")
	toolGit(t, mainRoot, "config", "user.email", "goast@example.invalid")
	toolGit(t, mainRoot, "config", "core.autocrlf", "false")
	toolWriteFile(t, filepath.Join(mainRoot, "go.mod"), "module example.com/worktree\n\ngo 1.24\n")
	toolWriteVariant(t, mainRoot, "main")
	toolGit(t, mainRoot, "add", "go.mod", "variant.go")
	toolGit(t, mainRoot, "commit", "-m", "main variant")
	toolGit(t, mainRoot, "worktree", "add", "-b", "feature", featureRoot)
	toolWriteVariant(t, featureRoot, "feature")
	toolGit(t, featureRoot, "add", "variant.go")
	toolGit(t, featureRoot, "commit", "-m", "feature variant")
	return toolWorktreeFixture{
		main:    toolCanonical(t, mainRoot),
		feature: toolCanonical(t, featureRoot),
		head:    toolGit(t, featureRoot, "rev-parse", "HEAD"),
	}
}

func newToolHolder(t *testing.T, root string) *index.IndexHolder {
	t.Helper()
	config := index.IndexConfig{
		Repos: []index.RepoConfig{{
			Name:                  "logical-repository",
			Path:                  root,
			IncludeTests:          true,
			TypedMethodReferences: true,
		}},
	}
	idx, err := index.BuildIndex(config)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return index.NewHolder(idx, config)
}

func newToolSession(t *testing.T, holder *index.IndexHolder) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "worktree-test", Version: "0.0.1"}, nil)
	RegisterReindex(server, holder)
	RegisterIndexStatus(server, holder)
	RegisterReadSymbol(server, holder)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "worktree-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("session.Close: %v", err)
		}
	})
	return session
}

func callWorktreeTool(
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	arguments map[string]any,
) (*mcp.CallToolResult, string) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if value, ok := content.(*mcp.TextContent); ok {
			text.WriteString(value.Text)
		}
	}
	return result, text.String()
}

func TestReindexWorktreeRoundTripAndProvenance(t *testing.T) {
	fixture := newToolWorktreeFixture(t)
	holder := newToolHolder(t, fixture.main)
	session := newToolSession(t, holder)

	_, initial := callWorktreeTool(t, session, "index-status", nil)
	if !strings.Contains(initial, "active="+fixture.main) || !strings.Contains(initial, "worktree_override=false") {
		t.Fatalf("initial status does not identify configured worktree:\n%s", initial)
	}

	result, switched := callWorktreeTool(t, session, "reindex", map[string]any{
		"worktree_root": fixture.feature,
	})
	if result.IsError {
		t.Fatalf("worktree reindex failed:\n%s", switched)
	}
	for _, expected := range []string{
		"Reindex complete",
		"Selected worktree: root=" + fixture.feature,
		"branch=feature",
		"head=" + fixture.head,
		"worktree_overrides=1",
	} {
		if !strings.Contains(switched, expected) {
			t.Errorf("reindex response missing %q:\n%s", expected, switched)
		}
	}

	_, source := callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/worktree",
		"name":    "Variant",
	})
	if !strings.Contains(source, fixture.feature) || !strings.Contains(source, `Variant = "feature"`) {
		t.Fatalf("read-symbol did not switch to feature source:\n%s", source)
	}

	result, persisted := callWorktreeTool(t, session, "reindex", nil)
	if result.IsError || !strings.Contains(persisted, "worktree_overrides=1") {
		t.Fatalf("argument-free reindex did not preserve worktree:\n%s", persisted)
	}
	if strings.Contains(persisted, "\nRepositories:\n") {
		t.Fatalf("routine reindex emitted the full repository inventory:\n%s", persisted)
	}
	_, persistedStatus := callWorktreeTool(t, session, "index-status", nil)
	if !strings.Contains(persistedStatus, "active="+fixture.feature) || !strings.Contains(persistedStatus, "worktree_override=true") {
		t.Fatalf("index-status did not retain selected worktree:\n%s", persistedStatus)
	}

	result, reset := callWorktreeTool(t, session, "reindex", map[string]any{
		"reset_worktrees": true,
	})
	if result.IsError || !strings.Contains(reset, "worktree_overrides=0") || !strings.Contains(reset, "Worktree overrides: none") {
		t.Fatalf("reset did not restore configured root:\n%s", reset)
	}
	_, resetStatus := callWorktreeTool(t, session, "index-status", nil)
	if !strings.Contains(resetStatus, "active="+fixture.main) || !strings.Contains(resetStatus, "worktree_override=false") {
		t.Fatalf("index-status did not return to configured root:\n%s", resetStatus)
	}
	_, source = callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/worktree",
		"name":    "Variant",
	})
	if !strings.Contains(source, fixture.main) || !strings.Contains(source, `Variant = "main"`) {
		t.Fatalf("read-symbol did not return to configured source:\n%s", source)
	}
}

func TestReindexRejectsUnrelatedRootWithoutPublishing(t *testing.T) {
	fixture := newToolWorktreeFixture(t)
	holder := newToolHolder(t, fixture.main)
	session := newToolSession(t, holder)
	_, before := callWorktreeTool(t, session, "index-status", nil)

	unrelated := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	toolGit(t, unrelated, "init")
	toolGit(t, unrelated, "config", "user.name", "GoAST Test")
	toolGit(t, unrelated, "config", "user.email", "goast@example.invalid")
	toolWriteFile(t, filepath.Join(unrelated, "go.mod"), "module example.com/worktree\n\ngo 1.24\n")
	toolWriteVariant(t, unrelated, "unrelated")
	toolGit(t, unrelated, "add", ".")
	toolGit(t, unrelated, "commit", "-m", "unrelated")

	result, failure := callWorktreeTool(t, session, "reindex", map[string]any{
		"worktree_root": toolCanonical(t, unrelated),
	})
	if !result.IsError || !strings.Contains(failure, "unrelated") {
		t.Fatalf("unrelated worktree was not rejected:\n%s", failure)
	}
	_, after := callWorktreeTool(t, session, "index-status", nil)
	if after != before {
		t.Fatalf("failed reindex changed status:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	_, source := callWorktreeTool(t, session, "read-symbol", map[string]any{
		"package": "example.com/worktree",
		"name":    "Variant",
	})
	if !strings.Contains(source, `Variant = "main"`) {
		t.Fatalf("failed reindex changed authoritative source:\n%s", source)
	}
}

func toolWriteVariant(t *testing.T, root, variant string) {
	t.Helper()
	toolWriteFile(t, filepath.Join(root, "variant.go"), fmt.Sprintf(
		"package worktree\n\n// Variant identifies the indexed checkout.\nconst Variant = %q\n",
		variant,
	))
}

func toolWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func toolGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func toolCanonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(absolute)
}
