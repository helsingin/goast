package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// Config holds the server configuration.
type Config struct {
	Repos           []RepoConfig `yaml:"repos"`
	ExcludePatterns []string     `yaml:"exclude_patterns"`
	Transport       string       `yaml:"transport"`
	Port            int          `yaml:"port"`
}

// RepoConfig describes a repository to index.
type RepoConfig struct {
	Path string `yaml:"path"`
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
		cfg.ExcludePatterns = []string{"vendor/**", "**/*_test.go", "testdata/**"}
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
