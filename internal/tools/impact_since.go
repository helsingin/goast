package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ImpactSinceArgs struct {
	Repository string `json:"repository" jsonschema:"required,Stable configured repository name"`
	BaseCommit string `json:"base_commit" jsonschema:"required,Git commit or revision used as the structural comparison baseline"`
}

func RegisterImpactSince(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "impact-since",
		Description: "Compare one configured repository's exact current indexed worktree with a Git base revision. " +
			"Returns added, modified, and deleted symbols; callers; implicit interfaces; config structs; services; " +
			"candidate tests; cross-repository dependants; source identity; and a deterministic impact digest. " +
			"Fails when the worktree no longer matches the published Goast generation.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ImpactSinceArgs) (*mcp.CallToolResult, any, error) {
		report, err := holder.ImpactSince(strings.TrimSpace(args.Repository), strings.TrimSpace(args.BaseCommit))
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatImpactReport(report)}},
		}, report, nil
	})
}

func formatImpactReport(report index.ImpactReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Repository: %s\nBase: %s\nHead: %s\n", report.Snapshot.Repository, report.Snapshot.BaseCommit, report.Snapshot.HeadCommit)
	fmt.Fprintf(&output, "Goast generation: %d\nWorktree digest: %s\n", report.Snapshot.Generation, report.Snapshot.WorktreeDigest)
	fmt.Fprintf(&output, "Changed files: %d\nChanged symbols: %d\nAffected symbols: %d\n", len(report.ChangedFiles), len(report.ChangedSymbols), len(report.AffectedSymbols))
	fmt.Fprintf(&output, "Interfaces: %d | Config structs: %d | Services: %d | Candidate tests: %d\n",
		len(report.Interfaces), len(report.ConfigStructs), len(report.Services), len(report.CandidateTests))
	fmt.Fprintf(&output, "Affected repositories: %s\nImpact digest: %s\n", strings.Join(report.AffectedRepositories, ", "), report.ImpactDigest)
	for _, change := range report.ChangedSymbols {
		fmt.Fprintf(&output, "- %s %s | %s | %s.%s\n", change.Change, change.Symbol.Kind, change.Symbol.Repository, change.Symbol.Package, change.Symbol.Name)
	}
	return output.String()
}
