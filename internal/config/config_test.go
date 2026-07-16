package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadIncludeTestsDefaultsAndRepositoryOverride(t *testing.T) {
	configPath := writeConfig(t, `
include_tests: true
repos:
  - path: /repo/inherit
  - path: /repo/disable
    include_tests: false
`)
	t.Setenv("GOAST_CONFIG", configPath)
	t.Setenv("GOAST_REPOS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Repos[0].TestsEnabled(cfg.IncludeTests) {
		t.Error("first repository should inherit include_tests: true")
	}
	if cfg.Repos[1].TestsEnabled(cfg.IncludeTests) {
		t.Error("second repository should override include_tests to false")
	}
	if slices.Contains(cfg.ExcludePatterns, "**/*_test.go") {
		t.Error("implicit exclude patterns must not override repository-specific test inclusion")
	}
}

func TestLoadIncludeTestsIsOptIn(t *testing.T) {
	configPath := writeConfig(t, `
repos:
  - path: /repo/default
  - path: /repo/enable
    include_tests: true
`)
	t.Setenv("GOAST_CONFIG", configPath)
	t.Setenv("GOAST_REPOS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Repos[0].TestsEnabled(cfg.IncludeTests) {
		t.Error("test indexing must remain disabled by default")
	}
	if !cfg.Repos[1].TestsEnabled(cfg.IncludeTests) {
		t.Error("repository-specific include_tests: true was ignored")
	}
}

func TestLoadTypedMethodReferencesIsOptInAndOverridable(t *testing.T) {
	configPath := writeConfig(t, `
typed_method_references: false
repos:
  - path: /repo/default
  - path: /repo/enable
    typed_method_references: true
build_contexts:
  - goos: linux
    goarch: amd64
    cgo_enabled: false
    build_tags: [race]
`)
	t.Setenv("GOAST_CONFIG", configPath)
	t.Setenv("GOAST_REPOS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Repos[0].TypedReferencesEnabled(cfg.TypedMethodReferences) {
		t.Error("typed method references must remain disabled by default")
	}
	if !cfg.Repos[1].TypedReferencesEnabled(cfg.TypedMethodReferences) {
		t.Error("repository-specific typed method reference opt-in was ignored")
	}
	if len(cfg.BuildContexts) != 1 || cfg.BuildContexts[0].GOOS != "linux" || cfg.BuildContexts[0].GOARCH != "amd64" {
		t.Fatalf("build contexts were not decoded: %+v", cfg.BuildContexts)
	}
	if cfg.BuildContexts[0].CGOEnabled == nil || *cfg.BuildContexts[0].CGOEnabled {
		t.Fatalf("cgo_enabled: false was not preserved: %+v", cfg.BuildContexts[0])
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
