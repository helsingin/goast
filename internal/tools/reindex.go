package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReindexArgs optionally selects or clears a process-local Git worktree.
type ReindexArgs struct {
	WorktreeRoot   string `json:"worktree_root,omitempty" jsonschema:"Absolute root of a registered Git worktree belonging to a configured repository. The successful override persists across later reindexes."`
	ResetWorktrees bool   `json:"reset_worktrees,omitempty" jsonschema:"Clear every process-local worktree override and reindex the configured repository roots. Mutually exclusive with worktree_root."`
}

// RegisterReindex registers the reindex tool on the server.
func RegisterReindex(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reindex",
		Description: "Re-scan all repositories and atomically rebuild the symbol index. Pass worktree_root to switch this live server process to a registered worktree of a configured Git repository without restarting the MCP client; the selection persists across later argument-free reindexes. Pass reset_worktrees to return to configured roots. Call after source edits so declarations, references, and line numbers stay current.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ReindexArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		result, err := holder.ReindexWithOptions(index.ReindexOptions{
			WorktreeRoot:   args.WorktreeRoot,
			ResetWorktrees: args.ResetWorktrees,
		})
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Reindex failed: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}
		elapsed := time.Since(start)

		text := fmt.Sprintf(
			"Reindex complete: %d symbols in %d packages (%.1fs); generation=%d; worktree_overrides=%d",
			result.Symbols,
			result.Packages,
			elapsed.Seconds(),
			result.Status.Generation,
			len(result.Status.Worktrees),
		)
		if args.WorktreeRoot != "" {
			if selected, ok := findSelectedWorktree(result.Status, args.WorktreeRoot); ok {
				text += fmt.Sprintf(
					"\nSelected worktree: root=%s branch=%s head=%s",
					selected.Root,
					selected.Branch,
					selected.Head,
				)
			}
		}
		if args.ResetWorktrees {
			text += "\nWorktree overrides: none"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})
}

func findSelectedWorktree(status index.IndexStatus, requested string) (index.WorktreeStatus, bool) {
	canonical := requested
	if absolute, err := filepath.Abs(requested); err == nil {
		canonical = absolute
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	canonical = filepath.Clean(canonical)
	for _, worktree := range status.Worktrees {
		if filepath.Clean(worktree.Root) == canonical {
			return worktree, true
		}
	}
	return index.WorktreeStatus{}, false
}
