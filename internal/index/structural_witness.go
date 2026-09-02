package index

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type StructuralWitnessStatus string

const (
	StructuralWitnessComplete            StructuralWitnessStatus = "complete"
	StructuralWitnessCounterexample      StructuralWitnessStatus = "counterexample"
	StructuralWitnessNoWitnessDiscovered StructuralWitnessStatus = "no-witness-discovered"
	StructuralWitnessIndeterminate       StructuralWitnessStatus = "indeterminate"
	StructuralWitnessStale               StructuralWitnessStatus = "stale"
)

type StructuralCounterexampleCode string

const (
	StructuralBypass             StructuralCounterexampleCode = "BYPASS"
	StructuralIgnoredEnforcement StructuralCounterexampleCode = "IGNORED_ENFORCEMENT"
	StructuralBindingDrift       StructuralCounterexampleCode = "BINDING_DRIFT"
	StructuralFailureOpen        StructuralCounterexampleCode = "FAILURE_OPEN"
)

type StructuralScope struct {
	Repositories  []string `json:"repositories"`
	BuildContexts []string `json:"build_contexts"`
}

type StructuralWitnessRule struct {
	Version             int              `json:"version"`
	InvariantID         string           `json:"invariant_id"`
	Relation            string           `json:"relation"`
	Scope               StructuralScope  `json:"scope"`
	EntrySymbols        []SymbolIdentity `json:"entry_symbols"`
	EnforcementSymbols  []SymbolIdentity `json:"enforcement_symbols"`
	SinkSymbols         []SymbolIdentity `json:"sink_symbols"`
	PermitTypes         []SymbolIdentity `json:"permit_types,omitempty"`
	BindingRequirements []string         `json:"binding_requirements,omitempty"`
	FailurePolicy       string           `json:"failure_policy"`
}

type StructuralWitnessPath struct {
	BuildContext string         `json:"build_context"`
	Entry        SymbolIdentity `json:"entry"`
	Enforcement  SymbolIdentity `json:"enforcement"`
	Sink         SymbolIdentity `json:"sink"`
	Path         []string       `json:"path"`
}

type StructuralCounterexample struct {
	Code         StructuralCounterexampleCode `json:"code"`
	BuildContext string                       `json:"build_context"`
	Entry        SymbolIdentity               `json:"entry"`
	Sink         SymbolIdentity               `json:"sink"`
	Path         []string                     `json:"path"`
	Detail       string                       `json:"detail"`
}

type StructuralUnresolvedEdge struct {
	BuildContext string         `json:"build_context"`
	From         SymbolIdentity `json:"from"`
	Target       string         `json:"target"`
	Reason       string         `json:"reason"`
}

type StructuralWitnessReport struct {
	Version            int                        `json:"version"`
	InvariantID        string                     `json:"invariant_id"`
	Snapshot           SourceSnapshot             `json:"snapshot"`
	GoastGeneration    uint64                     `json:"goast_generation"`
	AnalysisMode       string                     `json:"analysis_mode"`
	RuleDigest         string                     `json:"rule_digest"`
	EntrySymbols       []SymbolIdentity           `json:"entry_symbols"`
	EnforcementSymbols []SymbolIdentity           `json:"enforcement_symbols"`
	SinkSymbols        []SymbolIdentity           `json:"sink_symbols"`
	DeclaredSinks      []SymbolIdentity           `json:"declared_sinks"`
	CandidateSinks     []SymbolIdentity           `json:"candidate_sinks"`
	WitnessedSinks     []StructuralWitnessPath    `json:"witnessed_sinks"`
	Counterexamples    []StructuralCounterexample `json:"counterexamples"`
	UnresolvedEdges    []StructuralUnresolvedEdge `json:"unresolved_edges"`
	Limitations        []string                   `json:"limitations"`
	Status             StructuralWitnessStatus    `json:"status"`
	ReportDigest       string                     `json:"report_digest"`
}

// AnalyzeStructuralWitness performs a bounded interprocedural SSA analysis of
// one repository and refuses to analyze source that differs from the published
// Goast generation.
func (h *IndexHolder) AnalyzeStructuralWitness(rule StructuralWitnessRule) (StructuralWitnessReport, error) {
	if err := validateStructuralRule(rule); err != nil {
		return StructuralWitnessReport{}, err
	}
	h.mu.RLock()
	generation := h.generation
	var repository RepositoryStatus
	for _, candidate := range h.repositories {
		if candidate.Name == rule.Scope.Repositories[0] {
			repository = candidate
			break
		}
	}
	h.mu.RUnlock()
	if repository.Name == "" {
		return StructuralWitnessReport{}, fmt.Errorf("repository %q is not in the published index", rule.Scope.Repositories[0])
	}
	if repository.GitRoot == "" || repository.WorktreeDigest == "" {
		return StructuralWitnessReport{}, fmt.Errorf("repository %q lacks a Git source snapshot", repository.Name)
	}
	current, err := captureCurrentSourceIdentity(repository.GitRoot)
	if err != nil {
		return StructuralWitnessReport{}, err
	}
	if current.HeadCommit != repository.Head || current.WorktreeDigest != repository.WorktreeDigest {
		return StructuralWitnessReport{}, fmt.Errorf("repository %q changed after Goast generation %d was published; reindex before structural analysis", repository.Name, generation)
	}

	report := StructuralWitnessReport{
		Version: 1, InvariantID: rule.InvariantID,
		Snapshot: SourceSnapshot{
			Repository: repository.Name, HeadCommit: repository.Head, Branch: repository.Branch,
			TrackedDiffDigest: repository.TrackedDiffDigest, UntrackedManifestDigest: repository.UntrackedDigest,
			WorktreeDigest: repository.WorktreeDigest, Generation: generation, StructuralProvider: "goast",
			StructuralGeneration: structuralProviderGeneration(generation, repository.ToolchainIdentity, repository.WorktreeDigest), ToolchainIdentity: repository.ToolchainIdentity,
		},
		GoastGeneration: generation, AnalysisMode: "bounded-interprocedural-ssa", RuleDigest: StructuralWitnessRuleDigest(rule),
		EntrySymbols: cloneSymbolIdentities(rule.EntrySymbols), EnforcementSymbols: cloneSymbolIdentities(rule.EnforcementSymbols),
		SinkSymbols: cloneSymbolIdentities(rule.SinkSymbols), DeclaredSinks: cloneSymbolIdentities(rule.SinkSymbols),
		Limitations: []string{"Analysis is bounded to registered entries, declared sinks, selected Go build contexts, and statically represented Go calls."},
	}

	for _, buildContext := range rule.Scope.BuildContexts {
		analysis, err := analyzeStructuralContext(repository.ActivePath, buildContext, rule)
		if err != nil {
			return StructuralWitnessReport{}, fmt.Errorf("analyze build context %q: %w", buildContext, err)
		}
		report.CandidateSinks = append(report.CandidateSinks, analysis.candidates...)
		report.WitnessedSinks = append(report.WitnessedSinks, analysis.witnesses...)
		report.Counterexamples = append(report.Counterexamples, analysis.counterexamples...)
		report.UnresolvedEdges = append(report.UnresolvedEdges, analysis.unresolved...)
	}
	normalizeStructuralReport(&report)
	switch {
	case len(report.Counterexamples) > 0:
		report.Status = StructuralWitnessCounterexample
	case len(report.UnresolvedEdges) > 0:
		report.Status = StructuralWitnessIndeterminate
	case len(report.WitnessedSinks) == 0:
		report.Status = StructuralWitnessNoWitnessDiscovered
	default:
		report.Status = StructuralWitnessComplete
	}
	report.ReportDigest = StructuralWitnessDigest(report)
	return report, nil
}

type structuralContextAnalysis struct {
	candidates      []SymbolIdentity
	witnesses       []StructuralWitnessPath
	counterexamples []StructuralCounterexample
	unresolved      []StructuralUnresolvedEdge
}

func analyzeStructuralContext(root, buildContext string, rule StructuralWitnessRule) (structuralContextAnalysis, error) {
	config := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
		Dir: root, Env: append([]string(nil), os.Environ()...), Tests: buildContext == "test" || buildContext == "tests",
	}
	if buildContext != "" && buildContext != "default" && buildContext != "test" && buildContext != "tests" {
		config.BuildFlags = []string{"-tags=" + buildContext}
	}
	loaded, err := packages.Load(config, "./...")
	if err != nil {
		return structuralContextAnalysis{}, err
	}
	if packages.PrintErrors(loaded) > 0 {
		return structuralContextAnalysis{}, fmt.Errorf("package loading failed")
	}
	program, _ := ssautil.AllPackages(loaded, ssa.InstantiateGenerics)
	program.Build()
	functions := ssautil.AllFunctions(program)
	byIdentity := make(map[string][]*ssa.Function)
	for function := range functions {
		if function == nil || function.Pkg == nil || function.Pkg.Pkg == nil || function.Synthetic != "" {
			continue
		}
		identity := structuralFunctionIdentity(function, rule.Scope.Repositories[0])
		byIdentity[structuralSymbolKey(identity)] = append(byIdentity[structuralSymbolKey(identity)], function)
	}

	result := structuralContextAnalysis{}
	for _, entry := range rule.EntrySymbols {
		entryFunctions := byIdentity[structuralSymbolKey(entry)]
		if len(entryFunctions) == 0 {
			result.unresolved = append(result.unresolved, StructuralUnresolvedEdge{
				BuildContext: buildContext, From: entry, Target: structuralSymbolDisplay(entry),
				Reason: "entry is absent from the selected build context",
			})
			continue
		}
		for _, function := range entryFunctions {
			visited := make(map[*ssa.Function]bool)
			analyzeReachableFunction(function, entry, buildContext, rule, visited, &result)
		}
	}
	return result, nil
}

func analyzeReachableFunction(function *ssa.Function, entry SymbolIdentity, buildContext string, rule StructuralWitnessRule, visited map[*ssa.Function]bool, result *structuralContextAnalysis) {
	if function == nil || visited[function] {
		return
	}
	visited[function] = true
	functionIdentity := structuralFunctionIdentity(function, rule.Scope.Repositories[0])

	var enforcements []*ssa.Call
	var sinks []*ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			if identity, found := structuralCallIdentity(call, rule.Scope.Repositories[0]); found {
				if structuralMatchesAny(identity, rule.EnforcementSymbols) {
					enforcements = append(enforcements, call)
				}
				if structuralMatchesAny(identity, rule.SinkSymbols) {
					sinks = append(sinks, call)
					result.candidates = append(result.candidates, canonicalDeclaredSink(identity, rule.SinkSymbols))
				} else if isCandidateEgress(identity) {
					result.candidates = append(result.candidates, identity)
				}
				if call.Common().IsInvoke() && !structuralMatchesAny(identity, rule.EnforcementSymbols) && !structuralMatchesAny(identity, rule.SinkSymbols) {
					result.unresolved = append(result.unresolved, StructuralUnresolvedEdge{
						BuildContext: buildContext, From: functionIdentity, Target: structuralSymbolDisplay(identity),
						Reason: "interface dispatch is outside the registered structural mapping",
					})
				}
			}
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Blocks != nil && callee.Pkg != nil && callee.Pkg.Pkg != nil && strings.HasPrefix(callee.Pkg.Pkg.Path(), modulePrefix(rule.EntrySymbols)) {
				analyzeReachableFunction(callee, entry, buildContext, rule, visited, result)
			}
		}
	}

	for _, sink := range sinks {
		sinkIdentity, _ := structuralCallIdentity(sink, rule.Scope.Repositories[0])
		sinkIdentity = canonicalDeclaredSink(sinkIdentity, rule.SinkSymbols)
		path := []string{structuralSymbolDisplay(entry)}
		if structuralSymbolKey(entry) != structuralSymbolKey(functionIdentity) {
			path = append(path, structuralSymbolDisplay(functionIdentity))
		}
		path = append(path, structuralSymbolDisplay(sinkIdentity))
		enforcement := closestDominatingEnforcement(enforcements, sink)
		if enforcement == nil {
			result.counterexamples = append(result.counterexamples, StructuralCounterexample{
				Code: StructuralBypass, BuildContext: buildContext, Entry: entry, Sink: sinkIdentity, Path: path,
				Detail: "sink is reachable without a dominating enforcement operation",
			})
			continue
		}
		enforcementIdentity, _ := structuralCallIdentity(enforcement, rule.Scope.Repositories[0])
		if !enforcementAffectsSink(enforcement, sink) {
			result.counterexamples = append(result.counterexamples, StructuralCounterexample{
				Code: StructuralIgnoredEnforcement, BuildContext: buildContext, Entry: entry, Sink: sinkIdentity, Path: path,
				Detail: "enforcement result neither gates nor supplies a permit to the sink",
			})
			continue
		}
		if missing := missingStructuralBindings(enforcement, sink, rule.BindingRequirements); len(missing) > 0 {
			result.counterexamples = append(result.counterexamples, StructuralCounterexample{
				Code: StructuralBindingDrift, BuildContext: buildContext, Entry: entry, Sink: sinkIdentity, Path: path,
				Detail: "sink arguments drift from enforcement binding: " + strings.Join(missing, ", "),
			})
			continue
		}
		if rule.FailurePolicy == "fail-closed" && enforcementFailureReachesSink(enforcement, sink) {
			result.counterexamples = append(result.counterexamples, StructuralCounterexample{
				Code: StructuralFailureOpen, BuildContext: buildContext, Entry: entry, Sink: sinkIdentity, Path: path,
				Detail: "enforcement failure branch reaches the sink",
			})
			continue
		}
		result.witnesses = append(result.witnesses, StructuralWitnessPath{
			BuildContext: buildContext, Entry: entry, Enforcement: enforcementIdentity, Sink: sinkIdentity,
			Path: append(path[:len(path)-1], structuralSymbolDisplay(enforcementIdentity), structuralSymbolDisplay(sinkIdentity)),
		})
	}
}

func closestDominatingEnforcement(enforcements []*ssa.Call, sink *ssa.Call) *ssa.Call {
	var result *ssa.Call
	for _, enforcement := range enforcements {
		if !instructionDominates(enforcement, sink) {
			continue
		}
		if result == nil || instructionDominates(result, enforcement) {
			result = enforcement
		}
	}
	return result
}

func instructionDominates(before, after ssa.Instruction) bool {
	if before.Block() == nil || after.Block() == nil {
		return false
	}
	if before.Block() != after.Block() {
		return before.Block().Dominates(after.Block())
	}
	beforeIndex, afterIndex := -1, -1
	for i, instruction := range before.Block().Instrs {
		if instruction == before {
			beforeIndex = i
		}
		if instruction == after {
			afterIndex = i
		}
	}
	return beforeIndex >= 0 && afterIndex >= 0 && beforeIndex < afterIndex
}

func enforcementAffectsSink(enforcement, sink *ssa.Call) bool {
	for _, argument := range sink.Common().Args {
		if structuralValueDependsOn(argument, enforcement, make(map[ssa.Value]bool)) {
			return true
		}
	}
	for _, block := range enforcement.Parent().Blocks {
		for _, instruction := range block.Instrs {
			branch, ok := instruction.(*ssa.If)
			if !ok || !structuralValueDependsOn(branch.Cond, enforcement, make(map[ssa.Value]bool)) {
				continue
			}
			if branchSeparatesSink(branch, sink.Block()) {
				return true
			}
		}
	}
	return false
}

func enforcementFailureReachesSink(enforcement, sink *ssa.Call) bool {
	for _, block := range enforcement.Parent().Blocks {
		for _, instruction := range block.Instrs {
			branch, ok := instruction.(*ssa.If)
			if !ok {
				continue
			}
			failureOnTrue, recognized := errorFailureBranch(branch.Cond, enforcement)
			if !recognized || len(block.Succs) != 2 {
				continue
			}
			trueReaches := blockReaches(block.Succs[0], sink.Block(), make(map[*ssa.BasicBlock]bool))
			falseReaches := blockReaches(block.Succs[1], sink.Block(), make(map[*ssa.BasicBlock]bool))
			if failureOnTrue && trueReaches || !failureOnTrue && falseReaches {
				return true
			}
			if trueReaches != falseReaches {
				return false
			}
		}
	}
	return enforcementReturnsError(enforcement)
}

func errorFailureBranch(condition ssa.Value, enforcement *ssa.Call) (bool, bool) {
	operation, ok := condition.(*ssa.BinOp)
	if !ok || operation.Op != token.NEQ && operation.Op != token.EQL {
		return false, false
	}
	var candidate ssa.Value
	if isNilSSAValue(operation.X) {
		candidate = operation.Y
	} else if isNilSSAValue(operation.Y) {
		candidate = operation.X
	} else {
		return false, false
	}
	if !isErrorExtractFrom(candidate, enforcement) {
		return false, false
	}
	return operation.Op == token.NEQ, true
}

func isErrorExtractFrom(value ssa.Value, enforcement *ssa.Call) bool {
	extract, ok := value.(*ssa.Extract)
	if !ok || !structuralValueDependsOn(extract.Tuple, enforcement, make(map[ssa.Value]bool)) {
		return false
	}
	return extract.Type() != nil && extract.Type().String() == "error"
}

func enforcementReturnsError(enforcement *ssa.Call) bool {
	signature := enforcement.Common().Signature()
	if signature == nil || signature.Results() == nil {
		return false
	}
	for i := 0; i < signature.Results().Len(); i++ {
		if signature.Results().At(i).Type().String() == "error" {
			return true
		}
	}
	return false
}

func branchSeparatesSink(branch *ssa.If, sink *ssa.BasicBlock) bool {
	if branch.Block() == nil || len(branch.Block().Succs) != 2 {
		return false
	}
	left := blockReaches(branch.Block().Succs[0], sink, make(map[*ssa.BasicBlock]bool))
	right := blockReaches(branch.Block().Succs[1], sink, make(map[*ssa.BasicBlock]bool))
	return left != right
}

func blockReaches(current, target *ssa.BasicBlock, visited map[*ssa.BasicBlock]bool) bool {
	if current == nil || visited[current] {
		return false
	}
	if current == target {
		return true
	}
	visited[current] = true
	for _, successor := range current.Succs {
		if blockReaches(successor, target, visited) {
			return true
		}
	}
	return false
}

func structuralValueDependsOn(value, target ssa.Value, visited map[ssa.Value]bool) bool {
	if value == nil || target == nil || visited[value] {
		return false
	}
	if value == target {
		return true
	}
	visited[value] = true
	instruction, ok := value.(ssa.Instruction)
	if !ok {
		return false
	}
	for _, operand := range instruction.Operands(nil) {
		if operand != nil && structuralValueDependsOn(*operand, target, visited) {
			return true
		}
	}
	return false
}

func missingStructuralBindings(enforcement, sink *ssa.Call, requirements []string) []string {
	missing := make([]string, 0)
	for _, requirement := range requirements {
		if !hasExactStructuralBinding(enforcement.Common().Args, sink.Common().Args, requirement) {
			missing = append(missing, requirement)
		}
	}
	return missing
}

func hasExactStructuralBinding(enforcementArgs, sinkArgs []ssa.Value, requirement string) bool {
	for _, enforcementArg := range enforcementArgs {
		if !bindingValueMatches(enforcementArg, requirement) {
			continue
		}
		for _, sinkArg := range sinkArgs {
			if enforcementArg == sinkArg {
				return true
			}
		}
	}
	return false
}

func bindingValueMatches(value ssa.Value, requirement string) bool {
	name := strings.ToLower(value.Name())
	typeName := ""
	if value.Type() != nil {
		typeName = strings.ToLower(value.Type().String())
	}
	switch requirement {
	case "payload-digest":
		return strings.Contains(name, "payload") || strings.Contains(name, "artifact") || strings.Contains(name, "message") || strings.Contains(typeName, "[]byte")
	case "destination":
		return strings.Contains(name, "destination") || strings.Contains(name, "target") || strings.Contains(name, "recipient") || strings.Contains(typeName, "sapientmessage")
	case "classification":
		return strings.Contains(name, "classification") || strings.Contains(name, "marking") || strings.Contains(typeName, "deliverymetadata")
	case "releasability":
		return strings.Contains(name, "releas") || strings.Contains(typeName, "deliverymetadata")
	case "policy-identity":
		return strings.Contains(name, "policy") || strings.Contains(typeName, "deliverymetadata")
	case "principal-identity":
		return strings.Contains(name, "principal") || strings.Contains(name, "subject") || strings.Contains(name, "identity") || strings.Contains(typeName, "sapientmessage") || strings.Contains(typeName, "deliverymetadata")
	default:
		return strings.Contains(name, strings.ReplaceAll(requirement, "-", ""))
	}
}

func structuralCallIdentity(call *ssa.Call, repository string) (SymbolIdentity, bool) {
	common := call.Common()
	if callee := common.StaticCallee(); callee != nil && callee.Pkg != nil && callee.Pkg.Pkg != nil {
		return structuralFunctionIdentity(callee, repository), true
	}
	if common.Method != nil && common.Method.Pkg() != nil {
		receiver := receiverTypeNameFromGoType(common.Method.Type().(*types.Signature).Recv().Type())
		return SymbolIdentity{Repository: repository, Language: "go", Package: common.Method.Pkg().Path(), Name: receiver + "." + common.Method.Name(), Kind: SymbolMethod}, true
	}
	return SymbolIdentity{}, false
}

func structuralFunctionIdentity(function *ssa.Function, repository string) SymbolIdentity {
	identity := SymbolIdentity{Repository: repository, Language: "go", Name: function.Name(), Kind: SymbolFunc}
	if function.Pkg != nil && function.Pkg.Pkg != nil {
		identity.Package = function.Pkg.Pkg.Path()
	}
	if function.Signature != nil && function.Signature.Recv() != nil {
		identity.Name = receiverTypeNameFromGoType(function.Signature.Recv().Type()) + "." + function.Name()
		identity.Kind = SymbolMethod
	}
	return identity
}

func receiverTypeNameFromGoType(value types.Type) string {
	for {
		switch typed := value.(type) {
		case *types.Pointer:
			value = typed.Elem()
		case *types.Named:
			return typed.Obj().Name()
		default:
			return strings.TrimPrefix(types.TypeString(value, func(*types.Package) string { return "" }), "*")
		}
	}
}

func validateStructuralRule(rule StructuralWitnessRule) error {
	if rule.Version != 1 {
		return fmt.Errorf("unsupported structural witness rule version %d", rule.Version)
	}
	if strings.TrimSpace(rule.InvariantID) == "" {
		return fmt.Errorf("invariant_id is required")
	}
	if rule.Relation != "must-pass-through" {
		return fmt.Errorf("unsupported structural relation %q", rule.Relation)
	}
	if len(rule.Scope.Repositories) != 1 {
		return fmt.Errorf("exactly one repository is required for bounded structural analysis")
	}
	if len(rule.Scope.BuildContexts) == 0 || len(rule.EntrySymbols) == 0 || len(rule.EnforcementSymbols) == 0 || len(rule.SinkSymbols) == 0 {
		return fmt.Errorf("build contexts, entries, enforcement symbols, and sinks are required")
	}
	for _, symbol := range append(append(append(append([]SymbolIdentity(nil), rule.EntrySymbols...), rule.EnforcementSymbols...), rule.SinkSymbols...), rule.PermitTypes...) {
		if symbol.Language != "go" {
			return fmt.Errorf("Goast structural symbols require language go, got %q for %s", symbol.Language, structuralSymbolDisplay(symbol))
		}
	}
	if rule.FailurePolicy != "fail-closed" {
		return fmt.Errorf("unsupported failure policy %q", rule.FailurePolicy)
	}
	return nil
}

func structuralMatchesAny(identity SymbolIdentity, candidates []SymbolIdentity) bool {
	for _, candidate := range candidates {
		if identity.Package != candidate.Package {
			continue
		}
		if identity.Name == candidate.Name {
			return true
		}
		identityParts := strings.Split(identity.Name, ".")
		candidateParts := strings.Split(candidate.Name, ".")
		if len(identityParts) == 2 && len(candidateParts) == 2 && identityParts[1] == candidateParts[1] {
			return true
		}
	}
	return false
}

func canonicalDeclaredSink(identity SymbolIdentity, declared []SymbolIdentity) SymbolIdentity {
	for _, candidate := range declared {
		if structuralMatchesAny(identity, []SymbolIdentity{candidate}) {
			return candidate
		}
	}
	return identity
}

func isCandidateEgress(identity SymbolIdentity) bool {
	name := identity.Name
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	switch strings.ToLower(name) {
	case "send", "publish", "produce", "forward", "write", "dispatch":
		return true
	default:
		return false
	}
}

func modulePrefix(entries []SymbolIdentity) string {
	if len(entries) == 0 {
		return ""
	}
	path := entries[0].Package
	parts := strings.Split(path, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[:2], "/")
	}
	return path
}

func structuralSymbolKey(symbol SymbolIdentity) string {
	return symbol.Repository + "\x00" + symbol.Language + "\x00" + symbol.Package + "\x00" + symbol.Name + "\x00" + string(symbol.Kind)
}

func structuralSymbolDisplay(symbol SymbolIdentity) string {
	return symbol.Repository + ":" + symbol.Package + "." + symbol.Name
}

func cloneSymbolIdentities(values []SymbolIdentity) []SymbolIdentity {
	return append([]SymbolIdentity(nil), values...)
}

func isNilSSAValue(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.IsNil()
}

func normalizeStructuralReport(report *StructuralWitnessReport) {
	sortSymbolIdentities(report.EntrySymbols)
	sortSymbolIdentities(report.EnforcementSymbols)
	sortSymbolIdentities(report.SinkSymbols)
	sortSymbolIdentities(report.DeclaredSinks)
	report.CandidateSinks = uniqueStructuralSymbols(report.CandidateSinks)
	sort.Slice(report.WitnessedSinks, func(i, j int) bool {
		return structuralWitnessSortKey(report.WitnessedSinks[i]) < structuralWitnessSortKey(report.WitnessedSinks[j])
	})
	sort.Slice(report.Counterexamples, func(i, j int) bool {
		return structuralCounterexampleSortKey(report.Counterexamples[i]) < structuralCounterexampleSortKey(report.Counterexamples[j])
	})
	sort.Slice(report.UnresolvedEdges, func(i, j int) bool {
		return structuralUnresolvedSortKey(report.UnresolvedEdges[i]) < structuralUnresolvedSortKey(report.UnresolvedEdges[j])
	})
	report.WitnessedSinks = uniqueStructuralWitnesses(report.WitnessedSinks)
	report.Counterexamples = uniqueStructuralCounterexamples(report.Counterexamples)
	report.UnresolvedEdges = uniqueStructuralUnresolved(report.UnresolvedEdges)
	sort.Strings(report.Limitations)
}

func sortSymbolIdentities(values []SymbolIdentity) {
	sort.Slice(values, func(i, j int) bool { return structuralSymbolKey(values[i]) < structuralSymbolKey(values[j]) })
}

func uniqueStructuralSymbols(values []SymbolIdentity) []SymbolIdentity {
	sortSymbolIdentities(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || structuralSymbolKey(result[len(result)-1]) != structuralSymbolKey(value) {
			result = append(result, value)
		}
	}
	return result
}

func structuralWitnessSortKey(value StructuralWitnessPath) string {
	return value.BuildContext + "\x00" + structuralSymbolKey(value.Entry) + "\x00" + structuralSymbolKey(value.Sink) + "\x00" + strings.Join(value.Path, "\x00")
}

func structuralCounterexampleSortKey(value StructuralCounterexample) string {
	return value.BuildContext + "\x00" + string(value.Code) + "\x00" + structuralSymbolKey(value.Entry) + "\x00" + structuralSymbolKey(value.Sink) + "\x00" + strings.Join(value.Path, "\x00")
}

func structuralUnresolvedSortKey(value StructuralUnresolvedEdge) string {
	return value.BuildContext + "\x00" + structuralSymbolKey(value.From) + "\x00" + value.Target + "\x00" + value.Reason
}

func uniqueStructuralWitnesses(values []StructuralWitnessPath) []StructuralWitnessPath {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || structuralWitnessSortKey(result[len(result)-1]) != structuralWitnessSortKey(value) {
			result = append(result, value)
		}
	}
	return result
}

func uniqueStructuralCounterexamples(values []StructuralCounterexample) []StructuralCounterexample {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || structuralCounterexampleSortKey(result[len(result)-1]) != structuralCounterexampleSortKey(value) {
			result = append(result, value)
		}
	}
	return result
}

func uniqueStructuralUnresolved(values []StructuralUnresolvedEdge) []StructuralUnresolvedEdge {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || structuralUnresolvedSortKey(result[len(result)-1]) != structuralUnresolvedSortKey(value) {
			result = append(result, value)
		}
	}
	return result
}

// StructuralWitnessRuleDigest binds a report to the complete frozen rule while
// excluding the separately carried invariant identity. Its JSON shape and
// canonical ordering match Canon's StructuralRuleDigest contract.
func StructuralWitnessRuleDigest(rule StructuralWitnessRule) string {
	type digestRule struct {
		Version             int              `json:"version"`
		Relation            string           `json:"relation"`
		Scope               StructuralScope  `json:"scope"`
		EntrySymbols        []SymbolIdentity `json:"entry_symbols"`
		EnforcementSymbols  []SymbolIdentity `json:"enforcement_symbols"`
		SinkSymbols         []SymbolIdentity `json:"sink_symbols"`
		PermitTypes         []SymbolIdentity `json:"permit_types,omitempty"`
		BindingRequirements []string         `json:"binding_requirements,omitempty"`
		FailurePolicy       string           `json:"failure_policy"`
	}
	canonical := digestRule{
		Version: rule.Version, Relation: rule.Relation,
		Scope: StructuralScope{
			Repositories:  uniqueSortedStructuralStrings(rule.Scope.Repositories),
			BuildContexts: uniqueSortedStructuralStrings(rule.Scope.BuildContexts),
		},
		EntrySymbols:        uniqueStructuralSymbols(cloneSymbolIdentities(rule.EntrySymbols)),
		EnforcementSymbols:  uniqueStructuralSymbols(cloneSymbolIdentities(rule.EnforcementSymbols)),
		SinkSymbols:         uniqueStructuralSymbols(cloneSymbolIdentities(rule.SinkSymbols)),
		PermitTypes:         uniqueStructuralSymbols(cloneSymbolIdentities(rule.PermitTypes)),
		BindingRequirements: uniqueSortedStructuralStrings(rule.BindingRequirements),
		FailurePolicy:       rule.FailurePolicy,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func uniqueSortedStructuralStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	out := result[:0]
	for _, value := range result {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

// StructuralWitnessDigest recomputes the report digest while excluding the
// digest field itself.
func StructuralWitnessDigest(report StructuralWitnessReport) string {
	report.ReportDigest = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}
