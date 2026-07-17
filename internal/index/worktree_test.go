package index

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	worktreeModulePath = "example.com/worktree"
	worktreeRepoName   = "stable-repository-name"
)

type worktreeFixture struct {
	git       string
	root      string
	primary   string
	linked    string
	unrelated string
	plain     string
	file      string

	primaryBranch string
	linkedBranch  string
	primaryHead   string
	linkedHead    string
}

func TestInspectWorktreeRoot(t *testing.T) {
	fixture := newWorktreeFixture(t)
	if !strings.Contains(fixture.root, " ") {
		t.Fatalf("fixture path must exercise spaces: %q", fixture.root)
	}

	primary, err := inspectWorktreeRoot(fixture.primary)
	if err != nil {
		t.Fatalf("inspect primary worktree: %v", err)
	}
	linked, err := inspectWorktreeRoot(fixture.linked)
	if err != nil {
		t.Fatalf("inspect linked worktree: %v", err)
	}

	assertWorktreeStatus(t, primary, fixture.primary, fixture.primaryBranch, fixture.primaryHead)
	assertWorktreeStatus(t, linked, fixture.linked, fixture.linkedBranch, fixture.linkedHead)
	if !samePath(primary.CommonDir, linked.CommonDir) {
		t.Fatalf("related worktrees have different common dirs: primary=%q linked=%q", primary.CommonDir, linked.CommonDir)
	}

	unrelated, err := inspectWorktreeRoot(fixture.unrelated)
	if err != nil {
		t.Fatalf("inspect unrelated repository root: %v", err)
	}
	if samePath(primary.CommonDir, unrelated.CommonDir) {
		t.Fatalf("independent repository unexpectedly shares common dir %q", primary.CommonDir)
	}

	unborn := filepath.Join(fixture.root, "unborn git repository")
	mustMkdirAll(t, unborn)
	worktreeRunGit(t, fixture.git, unborn, "-c", "init.templateDir=", "init", "-q", ".")

	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: filepath.Base(fixture.primary)},
		{name: "missing", path: filepath.Join(fixture.root, "missing worktree")},
		{name: "regular file", path: fixture.file},
		{name: "plain directory", path: fixture.plain},
		{name: "worktree subdirectory", path: filepath.Join(fixture.primary, "nested")},
		{name: "unborn repository", path: unborn},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if status, err := inspectWorktreeRoot(test.path); err == nil {
				t.Fatalf("inspectWorktreeRoot(%q) unexpectedly succeeded: %+v", test.path, status)
			}
		})
	}
}

func TestInspectWorktreeRootIgnoresAmbientGitRepositorySelection(t *testing.T) {
	fixture := newWorktreeFixture(t)
	unrelatedGitDir := filepath.Join(fixture.unrelated, ".git")
	for key, value := range map[string]string{
		"GIT_COMMON_DIR":       unrelatedGitDir,
		"GIT_DIR":              unrelatedGitDir,
		"GIT_INDEX_FILE":       filepath.Join(unrelatedGitDir, "index"),
		"GIT_OBJECT_DIRECTORY": filepath.Join(unrelatedGitDir, "objects"),
		"GIT_WORK_TREE":        fixture.unrelated,
	} {
		t.Setenv(key, value)
	}

	worktree, err := inspectWorktreeRoot(fixture.linked)
	if err != nil {
		t.Fatalf("inspect linked worktree with poisoned Git environment: %v", err)
	}
	assertWorktreeStatus(t, worktree, fixture.linked, fixture.linkedBranch, fixture.linkedHead)
}

func TestInspectWorktreeRootSkipsUnrelatedStaleRegistration(t *testing.T) {
	fixture := newWorktreeFixture(t)
	stale := filepath.Join(fixture.root, "stale linked worktree")
	worktreeRunGit(t, fixture.git, fixture.primary, "worktree", "add", "-q", "--detach", stale)
	if err := os.RemoveAll(stale); err != nil {
		t.Fatalf("remove stale worktree directory: %v", err)
	}

	worktree, err := inspectWorktreeRoot(fixture.linked)
	if err != nil {
		t.Fatalf("inspect valid worktree with unrelated stale registration: %v", err)
	}
	assertWorktreeStatus(t, worktree, fixture.linked, fixture.linkedBranch, fixture.linkedHead)
}

func TestInspectWorktreeRootReportsDetachedHead(t *testing.T) {
	fixture := newWorktreeFixture(t)
	detachedRoot := filepath.Join(fixture.root, "detached worktree")
	worktreeRunGit(t, fixture.git, fixture.primary, "worktree", "add", "-q", "--detach", detachedRoot)
	detachedRoot = canonicalTestDirectory(t, detachedRoot)
	wantHead := worktreeRunGit(t, fixture.git, detachedRoot, "rev-parse", "--verify", "HEAD")

	worktree, err := inspectWorktreeRoot(detachedRoot)
	if err != nil {
		t.Fatalf("inspect detached worktree: %v", err)
	}
	assertWorktreeStatus(t, worktree, detachedRoot, "(detached)", wantHead)
}

func TestApplyWorktreeOverridesMapsNestedModulesAndPreservesOptions(t *testing.T) {
	fixture := newWorktreeFixture(t)
	linked, err := inspectWorktreeRoot(fixture.linked)
	if err != nil {
		t.Fatalf("inspect linked worktree: %v", err)
	}

	cgoEnabled := false
	configured := IndexConfig{
		Repos: []RepoConfig{
			{
				Name:                  "root-module",
				Path:                  fixture.primary,
				IncludeTests:          true,
				TypedMethodReferences: false,
			},
			{
				Name:                  "nested-module",
				Path:                  filepath.Join(fixture.primary, "nested", "module"),
				IncludeTests:          false,
				TypedMethodReferences: true,
			},
			{
				Name:                  "plain-module",
				Path:                  fixture.plain,
				IncludeTests:          true,
				TypedMethodReferences: true,
			},
		},
		ExcludePatterns: []string{"vendor/**", "**/*_test.go"},
		BuildContexts: []BuildContext{{
			GOOS:       "linux",
			GOARCH:     "amd64",
			CGOEnabled: &cgoEnabled,
			BuildTags:  []string{"integration", "purego"},
			ToolTags:   []string{"goexperiment.example"},
		}},
	}
	before := cloneIndexConfig(configured)

	active, overrides, repositoryOverrides, err := applyWorktreeOverrides(
		configured,
		map[string]WorktreeStatus{linked.CommonDir: linked},
		linked.CommonDir,
	)
	if err != nil {
		t.Fatalf("applyWorktreeOverrides: %v", err)
	}

	if !reflect.DeepEqual(configured, before) {
		t.Fatalf("configured index config was mutated:\n got: %#v\nwant: %#v", configured, before)
	}
	if got, want := active.Repos[0].Path, fixture.linked; !samePath(got, want) {
		t.Errorf("root module active path: got %q, want %q", got, want)
	}
	linkedNested := filepath.Join(fixture.linked, "nested", "module")
	if got := active.Repos[1].Path; !samePath(got, linkedNested) {
		t.Errorf("nested module active path: got %q, want %q", got, linkedNested)
	}
	if got, want := active.Repos[2].Path, fixture.plain; got != want {
		t.Errorf("unrelated module path changed: got %q, want %q", got, want)
	}
	for i := range configured.Repos {
		got, want := active.Repos[i], configured.Repos[i]
		want.Path = active.Repos[i].Path
		if !reflect.DeepEqual(got, want) {
			t.Errorf("repository options %d changed: got %+v, want %+v", i, got, want)
		}
	}
	if !reflect.DeepEqual(active.ExcludePatterns, configured.ExcludePatterns) {
		t.Errorf("exclude patterns changed: got %v, want %v", active.ExcludePatterns, configured.ExcludePatterns)
	}
	if !reflect.DeepEqual(active.BuildContexts, configured.BuildContexts) {
		t.Errorf("build contexts changed: got %+v, want %+v", active.BuildContexts, configured.BuildContexts)
	}
	if active.BuildContexts[0].CGOEnabled == configured.BuildContexts[0].CGOEnabled {
		t.Error("active config retained the configured CGOEnabled pointer")
	}
	if len(overrides) != 1 {
		t.Fatalf("override count: got %d, want 1", len(overrides))
	}
	if got, want := repositoryOverrides, map[int]string{0: linked.CommonDir, 1: linked.CommonDir}; !reflect.DeepEqual(got, want) {
		t.Errorf("repository override ownership: got %v, want %v", got, want)
	}
	assertWorktreeStatus(t, overrides[linked.CommonDir], fixture.linked, fixture.linkedBranch, fixture.linkedHead)
}

func TestApplyWorktreeOverridesRejectsMissingMappedModule(t *testing.T) {
	fixture := newWorktreeFixture(t)
	linked, err := inspectWorktreeRoot(fixture.linked)
	if err != nil {
		t.Fatalf("inspect linked worktree: %v", err)
	}
	if err := os.Remove(filepath.Join(fixture.linked, "nested", "module", "go.mod")); err != nil {
		t.Fatalf("remove mapped go.mod: %v", err)
	}

	_, _, _, err = applyWorktreeOverrides(
		IndexConfig{Repos: []RepoConfig{{Path: filepath.Join(fixture.primary, "nested", "module")}}},
		map[string]WorktreeStatus{linked.CommonDir: linked},
		linked.CommonDir,
	)
	if err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("missing mapped module error = %v, want go.mod validation failure", err)
	}
}

func TestIndexHolderSwitchesWorktreesRoundTrip(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	if got, want := len(initial.Symbols), 2; got != want {
		t.Fatalf("initial symbol count: got %d, want %d", got, want)
	}
	if got, want := len(initial.Packages), 2; got != want {
		t.Fatalf("initial package count: got %d, want %d", got, want)
	}

	holder := NewHolder(initial, cfg)
	assertVariant(t, holder.Get(), fixture.primary, "A")
	assertPublishedRepository(t, holder.Status(), 1, fixture, fixture.primary, fixture.primaryBranch, fixture.primaryHead, false)

	toLinked, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked})
	if err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	assertVariant(t, holder.Get(), fixture.linked, "B")
	assertPublishedRepository(t, toLinked.Status, 2, fixture, fixture.linked, fixture.linkedBranch, fixture.linkedHead, true)
	if got := holder.Status(); !reflect.DeepEqual(got, toLinked.Status) {
		t.Fatalf("returned and published statuses differ:\nreturned: %+v\npublished: %+v", toLinked.Status, got)
	}

	symbols, packages, err := holder.Reindex()
	if err != nil {
		t.Fatalf("argument-free reindex: %v", err)
	}
	if symbols != 2 || packages != 2 {
		t.Fatalf("argument-free counts: got %d symbols/%d packages, want 2/2", symbols, packages)
	}
	assertVariant(t, holder.Get(), fixture.linked, "B")
	assertPublishedRepository(t, holder.Status(), 3, fixture, fixture.linked, fixture.linkedBranch, fixture.linkedHead, true)

	toPrimary, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.primary})
	if err != nil {
		t.Fatalf("switch back to primary worktree: %v", err)
	}
	assertVariant(t, holder.Get(), fixture.primary, "A")
	assertPublishedRepository(t, toPrimary.Status, 4, fixture, fixture.primary, fixture.primaryBranch, fixture.primaryHead, true)

	reset, err := holder.ReindexWithOptions(ReindexOptions{ResetWorktrees: true})
	if err != nil {
		t.Fatalf("reset worktrees: %v", err)
	}
	assertVariant(t, holder.Get(), fixture.primary, "A")
	assertPublishedRepository(t, reset.Status, 5, fixture, fixture.primary, fixture.primaryBranch, fixture.primaryHead, false)
}

func TestIndexHolderFreezesFallbackRepositoryNameAcrossWorktrees(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := IndexConfig{Repos: []RepoConfig{{Path: fixture.primary}}}
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	wantName := filepath.Base(fixture.primary)
	assertVariantWithRepository(t, holder.Get(), fixture.primary, "A", wantName)

	result, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked})
	if err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	assertVariantWithRepository(t, holder.Get(), fixture.linked, "B", wantName)
	if got := result.Status.Repositories[0].Name; got != wantName {
		t.Fatalf("published repository name: got %q, want %q", got, wantName)
	}
}

func TestIndexHolderConfigReloadPreservesActiveOverride(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	if _, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked}); err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	holder.SetConfigLoader(func() (IndexConfig, error) {
		fresh := worktreeIndexConfig(fixture.primary)
		fresh.Repos[0].IncludeTests = true
		return fresh, nil
	})

	result, err := holder.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		t.Fatalf("argument-free reindex after config reload: %v", err)
	}
	assertVariant(t, holder.Get(), fixture.linked, "B")
	if !result.Status.Repositories[0].IncludeTests || !result.Status.Repositories[0].WorktreeOverride {
		t.Fatalf("reloaded options/override not published together: %+v", result.Status.Repositories[0])
	}
}

func TestIndexHolderRequiresExplicitResetWhenOverrideOwnerIsRemoved(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	if _, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked}); err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	holder.SetConfigLoader(func() (IndexConfig, error) {
		return IndexConfig{Repos: []RepoConfig{{Name: "plain", Path: fixture.plain}}}, nil
	})
	before := snapshotHolder(holder)

	if _, err := holder.ReindexWithOptions(ReindexOptions{}); err == nil || !strings.Contains(err.Error(), "reset_worktrees") {
		t.Fatalf("owner-removal reindex error = %v, want explicit-reset error", err)
	}
	assertHolderSnapshot(t, holder, before)

	reset, err := holder.ReindexWithOptions(ReindexOptions{ResetWorktrees: true})
	if err != nil {
		t.Fatalf("explicit reset after owner removal: %v", err)
	}
	if len(reset.Status.Repositories) != 1 || reset.Status.Repositories[0].Name != "plain" || reset.Status.Repositories[0].ActivePath != fixture.plain {
		t.Fatalf("reset did not publish fresh configured repository: %+v", reset.Status.Repositories)
	}
	if len(reset.Status.Worktrees) != 0 {
		t.Fatalf("reset retained overrides: %+v", reset.Status.Worktrees)
	}
}

func TestIndexHolderRetainsPublishedStateWhenConfiguredRootCannotBeInspected(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	if _, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked}); err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	holder.SetConfigLoader(func() (IndexConfig, error) {
		return IndexConfig{Repos: []RepoConfig{{Path: filepath.Join(fixture.root, "temporarily missing root")}}}, nil
	})
	before := snapshotHolder(holder)

	if _, err := holder.ReindexWithOptions(ReindexOptions{}); err == nil || !strings.Contains(err.Error(), "reset_worktrees") {
		t.Fatalf("uninspectable configured-root error = %v, want retained-override failure", err)
	}
	assertHolderSnapshot(t, holder, before)
}

func TestIndexHolderRetainsPublishedStateWhenSelectedWorktreeDisappears(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	if _, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked}); err != nil {
		t.Fatalf("switch to linked worktree: %v", err)
	}
	before := snapshotHolder(holder)
	worktreeRunGit(t, fixture.git, fixture.primary, "worktree", "remove", "--force", fixture.linked)

	if _, err := holder.ReindexWithOptions(ReindexOptions{}); err == nil {
		t.Fatal("reindex unexpectedly succeeded after selected worktree disappeared")
	}
	assertHolderSnapshot(t, holder, before)
}

func TestIndexHolderRejectsHeadChangeDuringBuild(t *testing.T) {
	fixture := newWorktreeFixture(t)
	cfg := worktreeIndexConfig(fixture.primary)
	initial, err := BuildIndex(cfg)
	if err != nil {
		t.Fatalf("BuildIndex(primary): %v", err)
	}
	holder := NewHolder(initial, cfg)
	before := snapshotHolder(holder)
	started := make(chan struct{})
	release := make(chan struct{})
	holder.mu.Lock()
	holder.buildIndex = func(active IndexConfig) (*Index, error) {
		close(started)
		<-release
		return BuildIndex(active)
	}
	holder.mu.Unlock()

	outcome := make(chan error, 1)
	go func() {
		_, err := holder.ReindexWithOptions(ReindexOptions{WorktreeRoot: fixture.linked})
		outcome <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("rebuild did not reach blocked builder")
	}
	worktreeRunGit(t, fixture.git, fixture.linked, "commit", "--allow-empty", "-q", "-m", "advance during build")
	close(release)
	select {
	case err := <-outcome:
		if err == nil || !strings.Contains(err.Error(), "changed branch or HEAD during indexing") {
			t.Fatalf("mid-build HEAD-change error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reindex did not finish after releasing builder")
	}
	assertHolderSnapshot(t, holder, before)
}

func TestIndexHolderFailedReindexIsAtomic(t *testing.T) {
	fixture := newWorktreeFixture(t)
	sentinel := errors.New("sentinel rebuild failure")

	tests := []struct {
		name       string
		prepare    func(*IndexHolder)
		options    ReindexOptions
		wantError  error
		wantText   string
		checkAfter func(*testing.T, *IndexHolder)
	}{
		{
			name:     "invalid root",
			options:  ReindexOptions{WorktreeRoot: filepath.Join(fixture.root, "missing worktree")},
			wantText: "invalid worktree_root",
		},
		{
			name:     "unrelated root",
			options:  ReindexOptions{WorktreeRoot: fixture.unrelated},
			wantText: "unrelated to every configured repository",
		},
		{
			name: "loader failure",
			prepare: func(holder *IndexHolder) {
				holder.SetConfigLoader(func() (IndexConfig, error) {
					return IndexConfig{}, sentinel
				})
			},
			wantError: sentinel,
		},
		{
			name: "builder failure after validation",
			prepare: func(holder *IndexHolder) {
				holder.mu.Lock()
				holder.buildIndex = func(cfg IndexConfig) (*Index, error) {
					if len(cfg.Repos) != 1 || !samePath(cfg.Repos[0].Path, fixture.linked) {
						return nil, fmt.Errorf("builder received wrong active config: %+v", cfg.Repos)
					}
					return nil, sentinel
				}
				holder.mu.Unlock()
			},
			options:   ReindexOptions{WorktreeRoot: fixture.linked},
			wantError: sentinel,
			checkAfter: func(t *testing.T, holder *IndexHolder) {
				holder.mu.Lock()
				holder.buildIndex = BuildIndex
				holder.mu.Unlock()
				if _, _, err := holder.Reindex(); err != nil {
					t.Fatalf("reindex after restoring builder: %v", err)
				}
				assertVariant(t, holder.Get(), fixture.primary, "A")
				assertPublishedRepository(t, holder.Status(), 2, fixture, fixture.primary, fixture.primaryBranch, fixture.primaryHead, false)
			},
		},
		{
			name:     "mutually exclusive options",
			options:  ReindexOptions{WorktreeRoot: fixture.linked, ResetWorktrees: true},
			wantText: "mutually exclusive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := worktreeIndexConfig(fixture.primary)
			initial, err := BuildIndex(cfg)
			if err != nil {
				t.Fatalf("BuildIndex(primary): %v", err)
			}
			holder := NewHolder(initial, cfg)
			if test.prepare != nil {
				test.prepare(holder)
			}
			before := snapshotHolder(holder)

			_, err = holder.ReindexWithOptions(test.options)
			if err == nil {
				t.Fatal("ReindexWithOptions unexpectedly succeeded")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.wantError)
			}
			if test.wantText != "" && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("error = %q, want substring %q", err, test.wantText)
			}
			assertHolderSnapshot(t, holder, before)
			if test.checkAfter != nil {
				test.checkAfter(t, holder)
			}
		})
	}
}

func TestIndexHolderReadsContinueDuringBlockedRebuild(t *testing.T) {
	indexA := syntheticWorktreeIndex("A", 1, 1)
	indexB := syntheticWorktreeIndex("B", 3, 2)
	holder := NewHolder(indexA, IndexConfig{})

	started := make(chan struct{})
	release := make(chan struct{})
	holder.mu.Lock()
	holder.buildIndex = func(IndexConfig) (*Index, error) {
		close(started)
		<-release
		return indexB, nil
	}
	holder.mu.Unlock()

	type reindexOutcome struct {
		result ReindexResult
		err    error
	}
	outcome := make(chan reindexOutcome, 1)
	go func() {
		result, err := holder.ReindexWithOptions(ReindexOptions{})
		outcome <- reindexOutcome{result: result, err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("rebuild did not reach blocked builder")
	}

	getResult := make(chan *Index, 1)
	go func() { getResult <- holder.Get() }()
	select {
	case got := <-getResult:
		if got != indexA {
			t.Fatalf("Get during rebuild returned %p, want old index %p", got, indexA)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get blocked while replacement index was building")
	}

	statusResult := make(chan IndexStatus, 1)
	go func() { statusResult <- holder.Status() }()
	select {
	case got := <-statusResult:
		assertSyntheticStatus(t, got, 1, indexA)
	case <-time.After(2 * time.Second):
		t.Fatal("Status blocked while replacement index was building")
	}

	stopReaders := make(chan struct{})
	readerErrors := make(chan error, 1)
	var readers sync.WaitGroup
	for i := 0; i < 12; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				idx := holder.Get()
				if idx != indexA && idx != indexB {
					reportReaderError(readerErrors, fmt.Errorf("observed partial index pointer %p", idx))
					return
				}
				status := holder.Status()
				switch status.Generation {
				case 1:
					if status.Symbols != len(indexA.Symbols) || status.Packages != len(indexA.Packages) {
						reportReaderError(readerErrors, fmt.Errorf("mixed generation-1 status: %+v", status))
						return
					}
				case 2:
					if status.Symbols != len(indexB.Symbols) || status.Packages != len(indexB.Packages) {
						reportReaderError(readerErrors, fmt.Errorf("mixed generation-2 status: %+v", status))
						return
					}
				default:
					reportReaderError(readerErrors, fmt.Errorf("unexpected generation during rebuild: %+v", status))
					return
				}
			}
		}()
	}

	close(release)
	var completed reindexOutcome
	select {
	case completed = <-outcome:
	case <-time.After(5 * time.Second):
		close(stopReaders)
		readers.Wait()
		t.Fatal("reindex did not complete after releasing builder")
	}
	if completed.err != nil {
		close(stopReaders)
		readers.Wait()
		t.Fatalf("ReindexWithOptions: %v", completed.err)
	}
	if holder.Get() != indexB {
		t.Fatalf("published index = %p, want %p", holder.Get(), indexB)
	}
	assertSyntheticStatus(t, completed.result.Status, 2, indexB)
	assertSyntheticStatus(t, holder.Status(), 2, indexB)

	close(stopReaders)
	readers.Wait()
	select {
	case err := <-readerErrors:
		t.Fatal(err)
	default:
	}
}

func TestIndexHolderSerializesCompleteRebuilds(t *testing.T) {
	indexA := syntheticWorktreeIndex("A", 1, 1)
	indexB := syntheticWorktreeIndex("B", 2, 1)
	indexC := syntheticWorktreeIndex("C", 3, 2)
	holder := NewHolder(indexA, IndexConfig{})

	started := make(chan int, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var calls atomic.Int32
	holder.mu.Lock()
	holder.buildIndex = func(IndexConfig) (*Index, error) {
		call := int(calls.Add(1))
		started <- call
		<-releases[call-1]
		if call == 1 {
			return indexB, nil
		}
		return indexC, nil
	}
	holder.mu.Unlock()

	type outcome struct {
		result ReindexResult
		err    error
	}
	first := make(chan outcome, 1)
	second := make(chan outcome, 1)
	go func() {
		result, err := holder.ReindexWithOptions(ReindexOptions{})
		first <- outcome{result: result, err: err}
	}()
	select {
	case call := <-started:
		if call != 1 {
			t.Fatalf("first builder call = %d, want 1", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first rebuild did not start")
	}

	go func() {
		result, err := holder.ReindexWithOptions(ReindexOptions{})
		second <- outcome{result: result, err: err}
	}()
	select {
	case call := <-started:
		t.Fatalf("second builder call %d started before first rebuild published", call)
	case <-time.After(150 * time.Millisecond):
	}
	close(releases[0])

	var firstOutcome outcome
	select {
	case firstOutcome = <-first:
		if firstOutcome.err != nil {
			t.Fatalf("first reindex: %v", firstOutcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first reindex did not complete")
	}
	assertSyntheticStatus(t, firstOutcome.result.Status, 2, indexB)
	select {
	case call := <-started:
		if call != 2 {
			t.Fatalf("second builder call = %d, want 2", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second rebuild did not start after first published")
	}
	close(releases[1])

	select {
	case secondOutcome := <-second:
		if secondOutcome.err != nil {
			t.Fatalf("second reindex: %v", secondOutcome.err)
		}
		assertSyntheticStatus(t, secondOutcome.result.Status, 3, indexC)
	case <-time.After(5 * time.Second):
		t.Fatal("second reindex did not complete")
	}
	if holder.Get() != indexC {
		t.Fatalf("final index = %p, want second rebuild %p", holder.Get(), indexC)
	}
	assertSyntheticStatus(t, holder.Status(), 3, indexC)
}

type indexHolderSnapshot struct {
	index      *Index
	status     IndexStatus
	configured IndexConfig
	active     IndexConfig
	overrides  map[string]WorktreeStatus
}

func snapshotHolder(holder *IndexHolder) indexHolderSnapshot {
	status := holder.Status()
	holder.mu.RLock()
	defer holder.mu.RUnlock()
	return indexHolderSnapshot{
		index:      holder.idx,
		status:     status,
		configured: cloneIndexConfig(holder.configuredCfg),
		active:     cloneIndexConfig(holder.activeCfg),
		overrides:  cloneWorktreeOverrides(holder.overrides),
	}
}

func assertHolderSnapshot(t *testing.T, holder *IndexHolder, want indexHolderSnapshot) {
	t.Helper()
	got := snapshotHolder(holder)
	if got.index != want.index {
		t.Errorf("index pointer changed after failed rebuild: got %p, want %p", got.index, want.index)
	}
	if !reflect.DeepEqual(got.status, want.status) {
		t.Errorf("status changed after failed rebuild:\n got: %+v\nwant: %+v", got.status, want.status)
	}
	if !reflect.DeepEqual(got.configured, want.configured) {
		t.Errorf("configured config changed after failed rebuild:\n got: %#v\nwant: %#v", got.configured, want.configured)
	}
	if !reflect.DeepEqual(got.active, want.active) {
		t.Errorf("active config changed after failed rebuild:\n got: %#v\nwant: %#v", got.active, want.active)
	}
	if !reflect.DeepEqual(got.overrides, want.overrides) {
		t.Errorf("overrides changed after failed rebuild:\n got: %#v\nwant: %#v", got.overrides, want.overrides)
	}
}

func assertPublishedRepository(
	t *testing.T,
	status IndexStatus,
	wantGeneration uint64,
	fixture worktreeFixture,
	wantActive, wantBranch, wantHead string,
	wantOverride bool,
) {
	t.Helper()
	if status.Generation != wantGeneration {
		t.Errorf("generation: got %d, want %d", status.Generation, wantGeneration)
	}
	if status.Symbols != 2 || status.Packages != 2 {
		t.Errorf("counts: got %d symbols/%d packages, want 2/2", status.Symbols, status.Packages)
	}
	if len(status.Repositories) != 1 {
		t.Fatalf("repository status count: got %d, want 1", len(status.Repositories))
	}
	repository := status.Repositories[0]
	if repository.Name != worktreeRepoName {
		t.Errorf("repository name: got %q, want %q", repository.Name, worktreeRepoName)
	}
	if repository.ConfiguredPath != fixture.primary {
		t.Errorf("configured path: got %q, want %q", repository.ConfiguredPath, fixture.primary)
	}
	if !samePath(repository.ActivePath, wantActive) {
		t.Errorf("active path: got %q, want %q", repository.ActivePath, wantActive)
	}
	if !samePath(repository.GitRoot, wantActive) {
		t.Errorf("Git root: got %q, want %q", repository.GitRoot, wantActive)
	}
	if repository.Branch != wantBranch || repository.Head != wantHead {
		t.Errorf("Git provenance: got branch=%q head=%q, want branch=%q head=%q", repository.Branch, repository.Head, wantBranch, wantHead)
	}
	if repository.WorktreeOverride != wantOverride {
		t.Errorf("worktree override: got %t, want %t", repository.WorktreeOverride, wantOverride)
	}
	if repository.IncludeTests || repository.TypedMethodReferences {
		t.Errorf("repository indexing options changed: %+v", repository)
	}
	if repository.GitCommonDir == "" {
		t.Error("Git common dir is empty")
	}
	if wantOverride {
		if len(status.Worktrees) != 1 {
			t.Fatalf("worktree override status count: got %d, want 1", len(status.Worktrees))
		}
		assertWorktreeStatus(t, status.Worktrees[0], wantActive, wantBranch, wantHead)
		if !samePath(status.Worktrees[0].CommonDir, repository.GitCommonDir) {
			t.Errorf("repository and override common dirs differ: repository=%q override=%q", repository.GitCommonDir, status.Worktrees[0].CommonDir)
		}
	} else if len(status.Worktrees) != 0 {
		t.Errorf("unexpected worktree overrides after reset: %+v", status.Worktrees)
	}
}

func assertWorktreeStatus(t *testing.T, got WorktreeStatus, root, branch, head string) {
	t.Helper()
	if !samePath(got.Root, root) {
		t.Errorf("worktree root: got %q, want %q", got.Root, root)
	}
	if got.CommonDir == "" {
		t.Error("worktree common dir is empty")
	}
	if got.Branch != branch {
		t.Errorf("worktree branch: got %q, want %q", got.Branch, branch)
	}
	if got.Head != head {
		t.Errorf("worktree HEAD: got %q, want %q", got.Head, head)
	}
}

func assertVariant(t *testing.T, idx *Index, root, variant string) {
	t.Helper()
	assertVariantWithRepository(t, idx, root, variant, worktreeRepoName)
}

func assertVariantWithRepository(t *testing.T, idx *Index, root, variant, repository string) {
	t.Helper()
	symbol := idx.GetSymbol(worktreeModulePath, "Variant")
	if symbol == nil {
		t.Fatalf("Variant not found at import path %q", worktreeModulePath)
	}
	wantSignature := fmt.Sprintf("Variant = %q", variant)
	if symbol.Signature != wantSignature {
		t.Errorf("Variant signature: got %q, want %q", symbol.Signature, wantSignature)
	}
	if symbol.Repo != repository {
		t.Errorf("Variant repository: got %q, want %q", symbol.Repo, repository)
	}
	if !pathWithin(root, symbol.FilePath) {
		t.Errorf("Variant file %q is not within active root %q", symbol.FilePath, root)
	}
}

func assertSyntheticStatus(t *testing.T, status IndexStatus, generation uint64, idx *Index) {
	t.Helper()
	if status.Generation != generation || status.Symbols != len(idx.Symbols) || status.Packages != len(idx.Packages) {
		t.Fatalf(
			"status tuple: got generation=%d symbols=%d packages=%d, want generation=%d symbols=%d packages=%d",
			status.Generation,
			status.Symbols,
			status.Packages,
			generation,
			len(idx.Symbols),
			len(idx.Packages),
		)
	}
}

func syntheticWorktreeIndex(prefix string, symbolCount, packageCount int) *Index {
	symbols := make([]Symbol, symbolCount)
	for i := range symbols {
		symbols[i] = Symbol{
			Name:       fmt.Sprintf("%sSymbol%d", prefix, i),
			Kind:       SymbolConst,
			ImportPath: fmt.Sprintf("example.com/%s/package%d", strings.ToLower(prefix), i%packageCount),
			Repo:       prefix,
		}
	}
	packages := make([]Package, packageCount)
	for i := range packages {
		packages[i] = Package{
			ImportPath: fmt.Sprintf("example.com/%s/package%d", strings.ToLower(prefix), i),
			Name:       fmt.Sprintf("package%d", i),
			Repo:       prefix,
		}
	}
	return NewIndex(symbols, packages, nil)
}

func reportReaderError(errors chan<- error, err error) {
	select {
	case errors <- err:
	default:
	}
}

func worktreeIndexConfig(path string) IndexConfig {
	return IndexConfig{Repos: []RepoConfig{{Name: worktreeRepoName, Path: path}}}
}

func newWorktreeFixture(t *testing.T) worktreeFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is required for worktree tests")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "GoAST Tests")
	t.Setenv("GIT_AUTHOR_EMAIL", "goast-tests@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "GoAST Tests")
	t.Setenv("GIT_COMMITTER_EMAIL", "goast-tests@example.invalid")

	root := filepath.Join(t.TempDir(), "git fixture with spaces")
	mustMkdirAll(t, root)
	fixture := worktreeFixture{
		git:           git,
		root:          canonicalTestDirectory(t, root),
		primaryBranch: "branch-a",
		linkedBranch:  "branch-b",
	}
	fixture.primary = filepath.Join(fixture.root, "primary worktree A")
	fixture.linked = filepath.Join(fixture.root, "linked worktree B")
	fixture.unrelated = filepath.Join(fixture.root, "unrelated repository")
	fixture.plain = filepath.Join(fixture.root, "plain module")
	fixture.file = filepath.Join(fixture.root, "ordinary file")

	initializeFixtureRepository(t, fixture.git, fixture.primary, fixture.primaryBranch, "A")
	worktreeRunGit(t, fixture.git, fixture.primary, "worktree", "add", "-q", "-b", fixture.linkedBranch, fixture.linked)
	writeFixtureSources(t, fixture.linked, "B")
	worktreeRunGit(t, fixture.git, fixture.linked, "add", ".")
	worktreeRunGit(t, fixture.git, fixture.linked, "commit", "-q", "-m", "variant B")

	initializeFixtureRepository(t, fixture.git, fixture.unrelated, "unrelated-branch", "A")
	writeFixtureSources(t, fixture.plain, "plain")
	writeTestFile(t, fixture.file, "ordinary file\n")

	fixture.primary = canonicalTestDirectory(t, fixture.primary)
	fixture.linked = canonicalTestDirectory(t, fixture.linked)
	fixture.unrelated = canonicalTestDirectory(t, fixture.unrelated)
	fixture.plain = canonicalTestDirectory(t, fixture.plain)
	fixture.primaryHead = worktreeRunGit(t, fixture.git, fixture.primary, "rev-parse", "--verify", "HEAD")
	fixture.linkedHead = worktreeRunGit(t, fixture.git, fixture.linked, "rev-parse", "--verify", "HEAD")
	return fixture
}

func initializeFixtureRepository(t *testing.T, git, root, branch, variant string) {
	t.Helper()
	mustMkdirAll(t, root)
	worktreeRunGit(t, git, root, "-c", "init.templateDir=", "init", "-q", ".")
	worktreeRunGit(t, git, root, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	worktreeRunGit(t, git, root, "config", "user.name", "GoAST Tests")
	worktreeRunGit(t, git, root, "config", "user.email", "goast-tests@example.invalid")
	worktreeRunGit(t, git, root, "config", "commit.gpgsign", "false")
	worktreeRunGit(t, git, root, "config", "core.autocrlf", "false")
	writeFixtureSources(t, root, variant)
	worktreeRunGit(t, git, root, "add", ".")
	worktreeRunGit(t, git, root, "commit", "-q", "-m", "variant "+variant)
}

func writeFixtureSources(t *testing.T, root, variant string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module "+worktreeModulePath+"\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(root, "variant.go"), fmt.Sprintf("package fixture\n\nconst Variant = %q\n", variant))
	nested := filepath.Join(root, "nested", "module")
	writeTestFile(t, filepath.Join(nested, "go.mod"), "module "+worktreeModulePath+"/nested\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(nested, "variant.go"), fmt.Sprintf("package nested\n\nconst NestedVariant = %q\n", variant))
}

func worktreeRunGit(t *testing.T, git, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command(git, commandArgs...)
	command.Env = sanitizedGitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), directory, err, output)
	}
	return strings.TrimSpace(string(output))
}

func sanitizedGitEnvironment() []string {
	blocked := map[string]bool{
		"GIT_COMMON_DIR":       true,
		"GIT_DIR":              true,
		"GIT_INDEX_FILE":       true,
		"GIT_OBJECT_DIRECTORY": true,
		"GIT_WORK_TREE":        true,
	}
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			environment = append(environment, entry)
		}
	}
	return environment
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func canonicalTestDirectory(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("absolute path for %s: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatalf("resolve path %s: %v", absolute, err)
	}
	return filepath.Clean(resolved)
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
