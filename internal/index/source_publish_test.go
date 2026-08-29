package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildHolderPublishesOnlyTheSourceItIndexed(t *testing.T) {
	repository := t.TempDir()
	impactGit(t, repository, "init", "-q")
	impactGit(t, repository, "config", "user.name", "Source Publish Test")
	impactGit(t, repository, "config", "user.email", "source@example.invalid")
	writeImpactFile(t, repository, "go.mod", "module example.local/source\n\ngo 1.24\n")
	writeImpactFile(t, repository, "source.go", "package source\nfunc Value() int { return 1 }\n")
	impactGit(t, repository, "add", ".")
	impactGit(t, repository, "commit", "-q", "-m", "base")
	cfg := IndexConfig{Repos: []RepoConfig{{Name: "source", Path: repository}}}

	holder, err := BuildHolder(cfg)
	if err != nil {
		t.Fatalf("build holder: %v", err)
	}
	status := holder.Status()
	if len(status.Repositories) != 1 || status.Repositories[0].WorktreeDigest == "" || status.Generation != 1 {
		t.Fatalf("status = %#v", status)
	}

	_, err = buildHolderWith(cfg, func(active IndexConfig) (*Index, error) {
		idx, buildErr := BuildIndex(active)
		if buildErr != nil {
			return nil, buildErr
		}
		return idx, os.WriteFile(filepath.Join(repository, "source.go"), []byte("package source\nfunc Value() int { return 2 }\n"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "changed while its index was being built") {
		t.Fatalf("source mutation error = %v", err)
	}
}
