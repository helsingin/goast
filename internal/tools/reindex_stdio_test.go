package tools

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Opt-in acceptance/benchmark against a separately built server and real config.
func TestReindexStdioReuse(t *testing.T) {
	binary := os.Getenv("GOAST_REINDEX_TEST_BINARY")
	config := os.Getenv("GOAST_REINDEX_TEST_CONFIG")
	if binary == "" || config == "" {
		t.Skip("set GOAST_REINDEX_TEST_BINARY and GOAST_REINDEX_TEST_CONFIG")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.Command(binary)
	cmd.Dir = os.Getenv("GOAST_REINDEX_TEST_CWD")
	cmd.Env = append(os.Environ(), "GOAST_CONFIG="+config)
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "reindex-acceptance", Version: "1"}, nil)
	start := time.Now()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	t.Logf("startup: %s", time.Since(start))
	for range 3 {
		start = time.Now()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reindex", Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				output.WriteString(text.Text)
			}
		}
		t.Logf("reindex: %s: %s", time.Since(start), output.String())
		if result.IsError || (os.Getenv("GOAST_REINDEX_TEST_BASELINE") == "" && (!strings.Contains(output.String(), "unchanged=true") || !strings.Contains(output.String(), "generation=1;"))) {
			t.Fatalf("did not reuse initial generation: %s", output.String())
		}
	}
}
