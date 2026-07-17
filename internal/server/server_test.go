package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewRegistersWorktreeToolsAndSchema(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	repository := filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "sample_repo")
	config := index.IndexConfig{Repos: []index.RepoConfig{{Path: repository}}}
	idx, err := index.BuildIndex(config)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	server := New(index.NewHolder(idx, config))
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "server-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("session.Close: %v", err)
		}
	})

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	foundStatus := false
	foundReindex := false
	for _, tool := range result.Tools {
		switch tool.Name {
		case "index-status":
			foundStatus = true
		case "reindex":
			foundReindex = true
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal reindex schema: %v", err)
			}
			for _, field := range []string{"worktree_root", "reset_worktrees"} {
				if !strings.Contains(string(schema), field) {
					t.Errorf("reindex schema missing %s: %s", field, schema)
				}
			}
		}
	}
	if !foundStatus || !foundReindex {
		t.Fatalf("registered tools: index-status=%t reindex=%t", foundStatus, foundReindex)
	}
}
