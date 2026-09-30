package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AnalyzeStructuralWitnessArgs struct {
	Version             int                    `json:"version" jsonschema:"required,Structural witness rule schema version"`
	InvariantID         string                 `json:"invariant_id" jsonschema:"required,Frozen Canon invariant identity"`
	Relation            string                 `json:"relation" jsonschema:"required,Bounded relation; currently must-pass-through"`
	Scope               index.StructuralScope  `json:"scope" jsonschema:"required,Repository and build-context scope"`
	EntrySymbols        []index.SymbolIdentity `json:"entry_symbols" jsonschema:"required,Registered production entry symbols"`
	EnforcementSymbols  []index.SymbolIdentity `json:"enforcement_symbols" jsonschema:"required,Registered enforcement operations"`
	SinkSymbols         []index.SymbolIdentity `json:"sink_symbols" jsonschema:"required,Frozen sensitive sink universe"`
	PermitTypes         []index.SymbolIdentity `json:"permit_types,omitempty" jsonschema:"Capability types produced only by successful enforcement"`
	BindingRequirements []string               `json:"binding_requirements,omitempty" jsonschema:"Artifact and security-context values that must remain exactly bound"`
	FailurePolicy       string                 `json:"failure_policy" jsonschema:"required,Required enforcement failure behavior"`
}

func RegisterAnalyzeStructuralWitness(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "analyze-structural-witness",
		Description: "Evaluate one frozen must-pass-through structural invariant against an exact Goast source generation. " +
			"Returns deterministic source-bound witnesses, concrete bypass/binding/failure counterexamples, explicit unresolved edges, " +
			"and separate declared and candidate sink universes. Completeness requires a witness for every declared sink in every requested build context, " +
			"with no counterexample or unresolved edge.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args AnalyzeStructuralWitnessArgs) (*mcp.CallToolResult, any, error) {
		report, err := holder.AnalyzeStructuralWitness(index.StructuralWitnessRule{
			Version: args.Version, InvariantID: strings.TrimSpace(args.InvariantID), Relation: strings.TrimSpace(args.Relation),
			Scope: args.Scope, EntrySymbols: args.EntrySymbols, EnforcementSymbols: args.EnforcementSymbols,
			SinkSymbols: args.SinkSymbols, PermitTypes: args.PermitTypes, BindingRequirements: args.BindingRequirements,
			FailurePolicy: strings.TrimSpace(args.FailurePolicy),
		})
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: formatStructuralWitnessReport(report)}}}, report, nil
	})
}

func formatStructuralWitnessReport(report index.StructuralWitnessReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Invariant: %s\nStatus: %s\nRepository: %s\nHead: %s\nGoast generation: %d\n",
		report.InvariantID, report.Status, report.Snapshot.Repository, report.Snapshot.HeadCommit, report.GoastGeneration)
	fmt.Fprintf(&output, "Declared sinks: %d | Candidate sinks: %d | Witnesses: %d | Counterexamples: %d | Unresolved edges: %d\n",
		len(report.DeclaredSinks), len(report.CandidateSinks), len(report.WitnessedSinks), len(report.Counterexamples), len(report.UnresolvedEdges))
	for _, counterexample := range report.Counterexamples {
		fmt.Fprintf(&output, "- %s | %s | %s\n", counterexample.Code, strings.Join(counterexample.Path, " -> "), counterexample.Detail)
	}
	for _, unresolved := range report.UnresolvedEdges {
		fmt.Fprintf(&output, "- unresolved %s -> %s | %s\n", unresolved.From.Name, unresolved.Target, unresolved.Reason)
	}
	for _, limitation := range report.Limitations {
		fmt.Fprintf(&output, "- limitation: %s\n", limitation)
	}
	fmt.Fprintf(&output, "Report digest: %s", report.ReportDigest)
	return output.String()
}
