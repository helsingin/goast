package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// IndexStatusArgs are the input arguments for index-status.
type IndexStatusArgs struct{}

// RegisterIndexStatus registers immutable provenance for the published index.
func RegisterIndexStatus(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "index-status",
		Description: "Report the exact index generation currently visible to tools, including symbol/package counts, configured and active repository roots, and captured Git branch, HEAD, tracked, untracked, and combined worktree provenance.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args IndexStatusArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: formatIndexStatus(holder.Status())},
			},
		}, nil, nil
	})
}

func formatIndexStatus(status index.IndexStatus) string {
	var output strings.Builder
	fmt.Fprintf(
		&output,
		"Index generation %d: %d symbols in %d packages\nRepositories:\n",
		status.Generation,
		status.Symbols,
		status.Packages,
	)
	for _, repository := range status.Repositories {
		fmt.Fprintf(
			&output,
			"- %s: configured=%s active=%s include_tests=%t typed_method_references=%t",
			repository.Name,
			repository.ConfiguredPath,
			repository.ActivePath,
			repository.IncludeTests,
			repository.TypedMethodReferences,
		)
		if repository.GitRoot != "" {
			fmt.Fprintf(
				&output,
				" git_root=%s branch=%s head=%s worktree_digest=%s toolchain=%q worktree_override=%t",
				repository.GitRoot,
				repository.Branch,
				repository.Head,
				repository.WorktreeDigest,
				repository.ToolchainIdentity,
				repository.WorktreeOverride,
			)
		}
		output.WriteByte('\n')
	}
	if len(status.Worktrees) == 0 {
		output.WriteString("Worktree overrides: none")
		return output.String()
	}
	output.WriteString("Worktree overrides:\n")
	for i, worktree := range status.Worktrees {
		fmt.Fprintf(
			&output,
			"- root=%s branch=%s head=%s common_dir=%s",
			worktree.Root,
			worktree.Branch,
			worktree.Head,
			worktree.CommonDir,
		)
		if i+1 < len(status.Worktrees) {
			output.WriteByte('\n')
		}
	}
	return output.String()
}
