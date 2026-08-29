package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStructuralWitnessDistinguishesValidBypassDecorativeMutationAndFailurePaths(t *testing.T) {
	holder := structuralFixtureHolder(t)
	rule := structuralFixtureRule("Run")
	report, err := holder.AnalyzeStructuralWitness(rule)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StructuralWitnessCounterexample {
		t.Fatalf("status = %q, report = %#v", report.Status, report)
	}
	for _, code := range []StructuralCounterexampleCode{
		StructuralBypass,
		StructuralIgnoredEnforcement,
		StructuralBindingDrift,
		StructuralFailureOpen,
	} {
		if !hasStructuralCounterexample(report, code) {
			t.Errorf("missing counterexample %s: %#v", code, report.Counterexamples)
		}
	}
	if !hasStructuralWitness(report, "Sink.Send") {
		t.Errorf("valid interface-dispatched sink was not witnessed: %#v", report.WitnessedSinks)
	}
	alternate, err := holder.AnalyzeStructuralWitness(structuralFixtureRule("AlternateBypass"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasStructuralCandidate(alternate, "AlternateSink.Publish") {
		t.Errorf("alternate egress candidate was hidden: %#v", alternate.CandidateSinks)
	}

	for _, entry := range []string{"DestinationMutated", "Reused"} {
		report, err := holder.AnalyzeStructuralWitness(structuralFixtureRule(entry))
		if err != nil {
			t.Fatal(err)
		}
		if !hasStructuralCounterexample(report, StructuralBindingDrift) {
			t.Errorf("%s did not produce binding drift: %#v", entry, report)
		}
	}
}

func TestStructuralWitnessCompletesForExactProductionGateAndIsDeterministic(t *testing.T) {
	holder := structuralFixtureHolder(t)
	rule := structuralFixtureRule("Valid")
	first, err := holder.AnalyzeStructuralWitness(rule)
	if err != nil {
		t.Fatal(err)
	}
	second, err := holder.AnalyzeStructuralWitness(rule)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StructuralWitnessComplete || first.ReportDigest == "" {
		t.Fatalf("report not complete: %#v", first)
	}
	if first.RuleDigest == "" || first.RuleDigest != StructuralWitnessRuleDigest(rule) {
		t.Fatalf("report is not bound to the complete rule: %#v", first)
	}
	if first.ReportDigest != second.ReportDigest || first.ReportDigest != StructuralWitnessDigest(first) {
		t.Fatalf("non-deterministic digest: %q %q", first.ReportDigest, second.ReportDigest)
	}
	if len(first.CandidateSinks) == 0 || len(first.DeclaredSinks) != 1 {
		t.Fatalf("sink universes absent: %#v", first)
	}
}

func TestStructuralWitnessDoesNotUseTestOnlyOrExcludedBuildContext(t *testing.T) {
	holder := structuralFixtureHolder(t)
	for _, entry := range []string{"TestOnlyEntry", "ProductionOnly"} {
		rule := structuralFixtureRule(entry)
		report, err := holder.AnalyzeStructuralWitness(rule)
		if err != nil {
			t.Fatal(err)
		}
		if report.Status == StructuralWitnessComplete || len(report.UnresolvedEdges) == 0 {
			t.Errorf("excluded entry %s unexpectedly completed: %#v", entry, report)
		}
	}

	rule := structuralFixtureRule("ProductionOnly")
	rule.Scope.BuildContexts = []string{"production"}
	report, err := holder.AnalyzeStructuralWitness(rule)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StructuralWitnessComplete {
		t.Fatalf("production-tagged witness = %#v", report)
	}
}

func TestStructuralWitnessRejectsSourceDriftAfterPublishedGeneration(t *testing.T) {
	holder, root := structuralFixtureHolderWithRoot(t)
	path := filepath.Join(root, "release", "release.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// drift\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.AnalyzeStructuralWitness(structuralFixtureRule("Valid")); err == nil || !strings.Contains(err.Error(), "reindex") {
		t.Fatalf("source drift error = %v", err)
	}
}

func structuralFixtureHolder(t *testing.T) *IndexHolder {
	t.Helper()
	holder, _ := structuralFixtureHolderWithRoot(t)
	return holder
}

func structuralFixtureHolderWithRoot(t *testing.T) (*IndexHolder, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	_, filename, _, _ := runtime.Caller(0)
	source := filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "structural_witness")
	root := filepath.Join(t.TempDir(), "structural-witness")
	copyStructuralFixture(t, source, root)
	impactGit(t, root, "init", "-q")
	impactGit(t, root, "config", "user.name", "Structural Witness Test")
	impactGit(t, root, "config", "user.email", "witness@example.invalid")
	impactGit(t, root, "add", ".")
	impactGit(t, root, "commit", "-q", "-m", "fixture")
	cfg := IndexConfig{Repos: []RepoConfig{{Name: "fixture", Path: root, IncludeTests: true, TypedMethodReferences: true}}}
	holder, err := BuildHolder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return holder, root
}

func copyStructuralFixture(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func structuralFixtureRule(entry string) StructuralWitnessRule {
	return StructuralWitnessRule{
		Version:             1,
		InvariantID:         "INV-RELEASE-001",
		Relation:            "must-pass-through",
		Scope:               StructuralScope{Repositories: []string{"fixture"}, BuildContexts: []string{"default"}},
		EntrySymbols:        []SymbolIdentity{{Repository: "fixture", Package: "example.test/structural/release", Name: entry, Kind: SymbolFunc}},
		EnforcementSymbols:  []SymbolIdentity{{Repository: "fixture", Package: "example.test/structural/release", Name: "Authorize", Kind: SymbolFunc}},
		SinkSymbols:         []SymbolIdentity{{Repository: "fixture", Package: "example.test/structural/release", Name: "Sink.Send", Kind: SymbolMethod}},
		PermitTypes:         []SymbolIdentity{{Repository: "fixture", Package: "example.test/structural/release", Name: "Permit", Kind: SymbolType}},
		BindingRequirements: []string{"payload-digest", "destination"},
		FailurePolicy:       "fail-closed",
	}
}

func hasStructuralCounterexample(report StructuralWitnessReport, code StructuralCounterexampleCode) bool {
	for _, counterexample := range report.Counterexamples {
		if counterexample.Code == code {
			return true
		}
	}
	return false
}

func hasStructuralWitness(report StructuralWitnessReport, name string) bool {
	for _, witness := range report.WitnessedSinks {
		if witness.Sink.Name == name {
			return true
		}
	}
	return false
}

func hasStructuralCandidate(report StructuralWitnessReport, name string) bool {
	for _, candidate := range report.CandidateSinks {
		if candidate.Name == name {
			return true
		}
	}
	return false
}
