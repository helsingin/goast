package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// Config holds the server configuration.
type Config struct {
	Repos                 []RepoConfig         `yaml:"repos"`
	ExcludePatterns       []string             `yaml:"exclude_patterns"`
	IncludeTests          bool                 `yaml:"include_tests"`
	TypedMethodReferences bool                 `yaml:"typed_method_references"`
	BuildContexts         []BuildContextConfig `yaml:"build_contexts"`
	Transport             string               `yaml:"transport"`
	Port                  int                  `yaml:"port"`
}

// RepoConfig describes a repository to index.
type RepoConfig struct {
	Path                  string `yaml:"path"`
	IncludeTests          *bool  `yaml:"include_tests,omitempty"`
	TypedMethodReferences *bool  `yaml:"typed_method_references,omitempty"`
}

// BuildContextConfig selects one coherent Go build variant for typed method
// reference analysis. Omitted contexts default to the running Go toolchain's
// GOOS, GOARCH, cgo, release tags, and tool tags.
type BuildContextConfig struct {
	GOOS       string   `yaml:"goos"`
	GOARCH     string   `yaml:"goarch"`
	CGOEnabled *bool    `yaml:"cgo_enabled,omitempty"`
	BuildTags  []string `yaml:"build_tags,omitempty"`
	ToolTags   []string `yaml:"tool_tags,omitempty"`
}

// TestsEnabled returns the repository-specific test-indexing setting when one
// is present, otherwise the configuration-wide default.
func (r RepoConfig) TestsEnabled(defaultValue bool) bool {
	if r.IncludeTests != nil {
		return *r.IncludeTests
	}
	return defaultValue
}

// TypedReferencesEnabled returns the repository-specific typed-reference
// setting when one is present, otherwise the configuration-wide default.
func (r RepoConfig) TypedReferencesEnabled(defaultValue bool) bool {
	if r.TypedMethodReferences != nil {
		return *r.TypedMethodReferences
	}
	return defaultValue
}

// Load reads configuration from env vars or YAML file.
//
// Priority:
//  1. GOAST_CONFIG env var → load YAML file at that path
//  2. GOAST_REPOS env var → comma-separated repo paths
//  3. config.yaml in working directory
func Load() (*Config, error) {
	var cfg Config

	if configPath := os.Getenv("GOAST_CONFIG"); configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading config file %s: %w", configPath, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", configPath, err)
		}
	} else if reposEnv := os.Getenv("GOAST_REPOS"); reposEnv != "" {
		for _, p := range strings.Split(reposEnv, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Repos = append(cfg.Repos, RepoConfig{Path: p})
			}
		}
	} else {
		data, err := os.ReadFile("config.yaml")
		if err != nil {
			return nil, fmt.Errorf("no config found: set GOAST_CONFIG, GOAST_REPOS, or create config.yaml: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config.yaml: %w", err)
		}
	}

	// Apply defaults.
	if len(cfg.ExcludePatterns) == 0 {
		cfg.ExcludePatterns = []string{"vendor/**", "testdata/**"}
	}
	if cfg.Transport == "" {
		cfg.Transport = "stdio"
	}
	if cfg.Port <= 0 {
		cfg.Port = 7400
	}

	if len(cfg.Repos) == 0 {
		return nil, fmt.Errorf("no repos configured")
	}

	return &cfg, nil
}
