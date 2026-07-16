package main

import (
	"testing"

	"github.com/helsingin/goast/internal/config"
)

func TestMakeIndexConfigResolvesRepositoryTestOverrides(t *testing.T) {
	disabled := false
	enabled := true
	cfg := &config.Config{
		IncludeTests:          true,
		TypedMethodReferences: false,
		Repos: []config.RepoConfig{
			{Path: "/inherit"},
			{Path: "/override", IncludeTests: &disabled, TypedMethodReferences: &enabled},
		},
		ExcludePatterns: []string{"vendor/**"},
		BuildContexts: []config.BuildContextConfig{{
			GOOS:      "js",
			GOARCH:    "wasm",
			BuildTags: []string{"purego"},
		}},
	}

	got := makeIndexConfig(cfg)
	if len(got.Repos) != 2 {
		t.Fatalf("repository count: got %d, want 2", len(got.Repos))
	}
	if !got.Repos[0].IncludeTests {
		t.Error("first repository did not inherit include_tests: true")
	}
	if got.Repos[1].IncludeTests {
		t.Error("second repository did not apply include_tests: false override")
	}
	if got.Repos[0].TypedMethodReferences || !got.Repos[1].TypedMethodReferences {
		t.Errorf("typed method reference overrides were not resolved: %+v", got.Repos)
	}
	if len(got.BuildContexts) != 1 || got.BuildContexts[0].GOOS != "js" || got.BuildContexts[0].GOARCH != "wasm" {
		t.Fatalf("build context was not mapped: %+v", got.BuildContexts)
	}
}
