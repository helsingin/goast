package index

import (
	"go/build"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func reuseFixture(t *testing.T) (*IndexHolder, string) {
	t.Helper()
	root := t.TempDir()
	writeImpactFile(t, root, "go.mod", "module example.local/reuse\n\ngo 1.24\n")
	writeImpactFile(t, root, "source.go", "package reuse\nfunc Before() {}\n")
	holder, err := BuildHolder(IndexConfig{Repos: []RepoConfig{{Name: "reuse", Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	return holder, root
}

func TestUnchangedReindexReusesPublishedIndex(t *testing.T) {
	holder, _ := reuseFixture(t)
	original := holder.Get()
	var builds atomic.Int32
	holder.buildIndex = func(cfg IndexConfig) (*Index, error) { builds.Add(1); return BuildIndex(cfg) }
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := holder.ReindexWithOptions(ReindexOptions{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if builds.Load() != 0 || holder.Get() != original || holder.Status().Generation != 1 {
		t.Fatalf("unchanged requests rebuilt: builds=%d generation=%d same_index=%t", builds.Load(), holder.Status().Generation, holder.Get() == original)
	}
}

func TestReindexInputChangesInvalidateReuse(t *testing.T) {
	for _, change := range []string{"same-size-edit", "add", "delete", "module", "config"} {
		t.Run(change, func(t *testing.T) {
			holder, root := reuseFixture(t)
			original := holder.Get()
			switch change {
			case "same-size-edit":
				path := filepath.Join(root, "source.go")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				writeImpactFile(t, root, "source.go", "package reuse\nfunc After_() {}\n")
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "add":
				writeImpactFile(t, root, "added.go", "package reuse\nfunc Added() {}\n")
			case "delete":
				if err := os.Remove(filepath.Join(root, "source.go")); err != nil {
					t.Fatal(err)
				}
			case "module":
				writeImpactFile(t, root, "go.mod", "module example.local/changed\n\ngo 1.24\n")
			case "config":
				holder.SetConfigLoader(func() (IndexConfig, error) {
					cfg := cloneIndexConfig(holder.configuredCfg)
					cfg.Repos[0].IncludeTests = true
					return cfg, nil
				})
			}
			if _, err := holder.ReindexWithOptions(ReindexOptions{}); err != nil {
				t.Fatal(err)
			}
			if holder.Get() == original || holder.Status().Generation != 2 {
				t.Fatal("changed input reused stale index")
			}
			next := holder.Get()
			if _, err := holder.ReindexWithOptions(ReindexOptions{}); err != nil {
				t.Fatal(err)
			}
			if holder.Get() != next || holder.Status().Generation != 2 {
				t.Fatal("unchanged follow-up rebuilt")
			}
		})
	}
}

func TestReindexIgnoredGoFilesAndSourceDrift(t *testing.T) {
	holder, root := reuseFixture(t)
	impactGit(t, root, "init", "-q")
	impactGit(t, root, "config", "user.name", "Reuse Test")
	impactGit(t, root, "config", "user.email", "test@example.invalid")
	writeImpactFile(t, root, ".gitignore", "ignored.go\n")
	impactGit(t, root, "add", ".")
	impactGit(t, root, "commit", "-qm", "base")
	if _, err := holder.ReindexWithOptions(ReindexOptions{}); err != nil {
		t.Fatal(err)
	}
	generation := holder.Status().Generation
	writeImpactFile(t, root, "ignored.go", "package reuse\nfunc IgnoredByGit() {}\n")
	result, err := holder.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.Status.Generation != generation+1 {
		t.Fatal("Git-ignored Go input did not invalidate")
	}
	original := holder.Get()
	writeImpactFile(t, root, "source.go", "package reuse\nfunc Changed() {}\n")
	holder.buildIndex = func(cfg IndexConfig) (*Index, error) {
		idx, err := BuildIndex(cfg)
		writeImpactFile(t, root, "ignored.go", "package reuse\nfunc ChangedDuringBuild() {}\n")
		return idx, err
	}
	if _, err := holder.ReindexWithOptions(ReindexOptions{}); err == nil {
		t.Fatal("published source that changed during build")
	}
	if holder.Get() != original {
		t.Fatal("failed rebuild changed published index")
	}
}

func TestTypedReindexReuse(t *testing.T) {
	_, root := reuseFixture(t)
	holder, err := BuildHolder(IndexConfig{Repos: []RepoConfig{{Name: "typed", Path: root, TypedMethodReferences: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureIndexInputs(holder.activeCfg); err != nil {
		t.Fatal(err)
	}
	result, err := holder.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused {
		t.Fatal("typed unchanged index rebuilt")
	}
}

func TestReindexExternalTypedInputsInvalidate(t *testing.T) {
	previous := build.Default
	build.Default.GOPATH = t.TempDir()
	t.Cleanup(func() { build.Default = previous })
	_, root := reuseFixture(t)
	cfg := IndexConfig{Repos: []RepoConfig{{Name: "typed", Path: root, TypedMethodReferences: true}}}
	before, err := captureIndexInputs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeImpactFile(t, build.Default.GOPATH, "src/example.local/dependency/source.go", "package dependency\nfunc Value() {}\n")
	after, err := captureIndexInputs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("newly resolvable external import did not invalidate")
	}
	writeImpactFile(t, build.Default.GOPATH, "src/example.local/dependency/source.go", "package dependency\nfunc Other() {}\n")
	changed, err := captureIndexInputs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed == after {
		t.Fatal("external source edit did not invalidate")
	}
}

func TestReindexSymlinkedSourceTargetInvalidates(t *testing.T) {
	_, root := reuseFixture(t)
	target := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(target, []byte("package reuse\nfunc TargetA() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked.go")); err != nil {
		t.Fatal(err)
	}
	holder, err := BuildHolder(IndexConfig{Repos: []RepoConfig{{Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package reuse\nfunc TargetB() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := holder.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.Status.Generation != 2 {
		t.Fatal("changed symlink target reused stale source")
	}
}

func TestReindexModuleReplacementInvalidates(t *testing.T) {
	root := t.TempDir()
	dependency := filepath.Join(root, "dependency")
	main := filepath.Join(root, "main")
	writeImpactFile(t, dependency, "go.mod", "module example.local/dependency\n\ngo 1.24\n")
	writeImpactFile(t, dependency, "source.go", "package dependency\nfunc Value() {}\n")
	writeImpactFile(t, main, "go.mod", "module example.local/main\n\ngo 1.24\nrequire example.local/dependency v0.0.0\nreplace example.local/dependency => ../dependency\n")
	t.Chdir(main)
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "on")
	before, err := digestImportModules()
	if err != nil {
		t.Fatal(err)
	}
	writeImpactFile(t, dependency, "source.go", "package dependency\nfunc Other() {}\n")
	after, err := digestImportModules()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("external module replacement edit did not invalidate")
	}
	unchanged, err := digestImportModules()
	if err != nil {
		t.Fatal(err)
	}
	if after != unchanged {
		t.Fatal("unchanged module graph fingerprint is unstable")
	}
}

func TestReindexIgnoresUntraversedDirectorySymlinks(t *testing.T) {
	holder, root := reuseFixture(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	result, err := holder.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused {
		t.Fatal("non-indexed directory symlink prevented reuse")
	}
}
