package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReindexArgs are the input arguments for the reindex tool (currently none).
type ReindexArgs struct{}

// RegisterReindex registers the reindex tool on the server.
func RegisterReindex(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reindex",
		Description: "Re-scan all repositories and rebuild the symbol index. Call this after making code changes (adding/removing/renaming functions, types, etc.) so that list-packages, search-symbols, and read-symbol reflect the latest code. Fast (~0.3s for 16 repos). When in doubt, reindex.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ReindexArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		symbols, packages, err := holder.Reindex()
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Reindex failed: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}
		elapsed := time.Since(start)

		text := fmt.Sprintf("Reindex complete: %d symbols in %d packages (%.1fs)", symbols, packages, elapsed.Seconds())
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})
}
