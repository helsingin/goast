package index

import (
	"fmt"
	"sort"
	"sync"
)

// ConfigLoader returns the current configured index roots. It is called on
// every reindex so edits to the YAML configuration are observed without a
// server restart.
type ConfigLoader func() (IndexConfig, error)

// ReindexOptions selects an optional process-local Git worktree override.
// WorktreeRoot persists across later argument-free reindexes. ResetWorktrees
// removes every active override and returns to the configured repository roots.
type ReindexOptions struct {
	WorktreeRoot   string
	ResetWorktrees bool
}

// RepositoryStatus records the configured and active root of one indexed Go
// module. A worktree override changes ActivePath only.
type RepositoryStatus struct {
	Name                  string
	ConfiguredPath        string
	ActivePath            string
	IncludeTests          bool
	TypedMethodReferences bool
	GitRoot               string
	GitCommonDir          string
	Branch                string
	Head                  string
	TrackedDiffDigest     string
	UntrackedDigest       string
	WorktreeDigest        string
	ToolchainIdentity     string
	WorktreeOverride      bool
}

// WorktreeStatus is Git provenance captured for one active override at the
// moment its index was published.
type WorktreeStatus struct {
	Root      string
	CommonDir string
	Branch    string
	Head      string
}

// IndexStatus describes the immutable index currently visible to every tool.
type IndexStatus struct {
	Generation   uint64
	Symbols      int
	Packages     int
	Repositories []RepositoryStatus
	Worktrees    []WorktreeStatus
}

// ReindexResult is returned only after a replacement index has been built and
// atomically published.
type ReindexResult struct {
	Symbols  int
	Packages int
	Status   IndexStatus
}

// IndexHolder owns one immutable index generation. Queries take a short read
// lock only to load its pointer. Rebuilds are serialized, performed without
// holding that lock, and published atomically after complete success.
type IndexHolder struct {
	mu        sync.RWMutex
	reindexMu sync.Mutex

	idx           *Index
	buildIndex    func(IndexConfig) (*Index, error)
	configuredCfg IndexConfig
	activeCfg     IndexConfig
	repositories  []RepositoryStatus
	loadConfig    ConfigLoader
	overrides     map[string]WorktreeStatus // canonical Git common dir -> captured provenance
	generation    uint64
}

// NewHolder creates an IndexHolder around an index already built from cfg.
func NewHolder(idx *Index, cfg IndexConfig) *IndexHolder {
	configured := freezeRepoNames(cfg)
	active := cloneIndexConfig(configured)
	return newHolder(idx, configured, active, captureRepositoryStatus(configured, active, nil, nil))
}

// BuildHolder builds the initial index and publishes it only when the source
// snapshot remains unchanged for the complete build.
func BuildHolder(cfg IndexConfig) (*IndexHolder, error) {
	return buildHolderWith(cfg, BuildIndex)
}

func buildHolderWith(cfg IndexConfig, build func(IndexConfig) (*Index, error)) (*IndexHolder, error) {
	configured := freezeRepoNames(cfg)
	active := cloneIndexConfig(configured)
	before := captureRepositoryStatus(configured, active, nil, nil)
	idx, err := build(active)
	if err != nil {
		return nil, err
	}
	after := captureRepositoryStatus(configured, active, nil, nil)
	if err := validateRepositorySourceStability(before, after); err != nil {
		return nil, err
	}
	return newHolder(idx, configured, active, after), nil
}

func newHolder(idx *Index, configured, active IndexConfig, repositories []RepositoryStatus) *IndexHolder {
	return &IndexHolder{
		idx:           idx,
		buildIndex:    BuildIndex,
		configuredCfg: configured,
		activeCfg:     active,
		repositories:  append([]RepositoryStatus(nil), repositories...),
		overrides:     make(map[string]WorktreeStatus),
		generation:    1,
	}
}

// SetConfigLoader installs the loader consulted by later reindexes.
func (h *IndexHolder) SetConfigLoader(loader ConfigLoader) {
	h.mu.Lock()
	h.loadConfig = loader
	h.mu.Unlock()
}

// Get returns the current immutable index. Callers should load it separately
// for each tool invocation rather than retain it across a reindex.
func (h *IndexHolder) Get() *Index {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.idx
}

// Status returns detached provenance for the currently published generation.
func (h *IndexHolder) Status() IndexStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return makeIndexStatus(h.generation, len(h.idx.Symbols), len(h.idx.Packages), h.repositories, h.overrides)
}

// Reindex rebuilds from the active roots and preserves any process-local
// worktree overrides selected by an earlier ReindexWithOptions call.
func (h *IndexHolder) Reindex() (symbols int, packages int, err error) {
	result, err := h.ReindexWithOptions(ReindexOptions{})
	if err != nil {
		return 0, 0, err
	}
	return result.Symbols, result.Packages, nil
}

// ReindexWithOptions reloads configuration, applies a validated worktree
// selection, builds a complete replacement index, and then publishes the
// index, roots, override state, and generation in one critical section.
// Every failure leaves the previous generation byte-for-byte authoritative.
func (h *IndexHolder) ReindexWithOptions(options ReindexOptions) (ReindexResult, error) {
	if options.WorktreeRoot != "" && options.ResetWorktrees {
		return ReindexResult{}, fmt.Errorf("worktree_root and reset_worktrees are mutually exclusive")
	}

	h.reindexMu.Lock()
	defer h.reindexMu.Unlock()

	h.mu.RLock()
	configured := cloneIndexConfig(h.configuredCfg)
	loader := h.loadConfig
	buildIndex := h.buildIndex
	overrides := cloneWorktreeOverrides(h.overrides)
	h.mu.RUnlock()

	if loader != nil {
		fresh, err := loader()
		if err != nil {
			return ReindexResult{}, fmt.Errorf("load index configuration: %w", err)
		}
		configured = freezeRepoNames(fresh)
	}

	if options.ResetWorktrees {
		overrides = make(map[string]WorktreeStatus)
	}
	requiredCommonDir := ""
	if options.WorktreeRoot != "" {
		worktree, err := inspectWorktreeRoot(options.WorktreeRoot)
		if err != nil {
			return ReindexResult{}, err
		}
		overrides[worktree.CommonDir] = worktree
		requiredCommonDir = worktree.CommonDir
	}

	active, refreshedOverrides, repositoryOverrides, err := applyWorktreeOverrides(
		configured,
		overrides,
		requiredCommonDir,
	)
	if err != nil {
		return ReindexResult{}, err
	}
	beforeRepositories := captureRepositoryStatus(configured, active, refreshedOverrides, repositoryOverrides)

	newIdx, err := buildIndex(active)
	if err != nil {
		return ReindexResult{}, err
	}
	refreshedOverrides, err = validateWorktreeProvenance(refreshedOverrides)
	if err != nil {
		return ReindexResult{}, err
	}
	repositories := captureRepositoryStatus(configured, active, refreshedOverrides, repositoryOverrides)
	if err := validateRepositorySourceStability(beforeRepositories, repositories); err != nil {
		return ReindexResult{}, err
	}

	h.mu.Lock()
	h.idx = newIdx
	h.configuredCfg = cloneIndexConfig(configured)
	h.activeCfg = cloneIndexConfig(active)
	h.repositories = append([]RepositoryStatus(nil), repositories...)
	h.overrides = cloneWorktreeOverrides(refreshedOverrides)
	h.generation++
	status := makeIndexStatus(h.generation, len(newIdx.Symbols), len(newIdx.Packages), h.repositories, h.overrides)
	h.mu.Unlock()

	return ReindexResult{
		Symbols:  len(newIdx.Symbols),
		Packages: len(newIdx.Packages),
		Status:   status,
	}, nil
}

func makeIndexStatus(
	generation uint64,
	symbols, packages int,
	repositories []RepositoryStatus,
	overrides map[string]WorktreeStatus,
) IndexStatus {
	status := IndexStatus{Generation: generation, Symbols: symbols, Packages: packages}
	status.Repositories = append([]RepositoryStatus(nil), repositories...)

	keys := make([]string, 0, len(overrides))
	for commonDir := range overrides {
		keys = append(keys, commonDir)
	}
	sort.Strings(keys)
	status.Worktrees = make([]WorktreeStatus, 0, len(keys))
	for _, commonDir := range keys {
		status.Worktrees = append(status.Worktrees, overrides[commonDir])
	}
	return status
}

func cloneWorktreeOverrides(source map[string]WorktreeStatus) map[string]WorktreeStatus {
	cloned := make(map[string]WorktreeStatus, len(source))
	for commonDir, worktree := range source {
		cloned[commonDir] = worktree
	}
	return cloned
}

func cloneIndexConfig(source IndexConfig) IndexConfig {
	cloned := IndexConfig{
		Repos:           append([]RepoConfig(nil), source.Repos...),
		ExcludePatterns: append([]string(nil), source.ExcludePatterns...),
		BuildContexts:   make([]BuildContext, len(source.BuildContexts)),
	}
	for i, buildContext := range source.BuildContexts {
		cloned.BuildContexts[i] = buildContext
		cloned.BuildContexts[i].BuildTags = append([]string(nil), buildContext.BuildTags...)
		cloned.BuildContexts[i].ToolTags = append([]string(nil), buildContext.ToolTags...)
		if buildContext.CGOEnabled != nil {
			value := *buildContext.CGOEnabled
			cloned.BuildContexts[i].CGOEnabled = &value
		}
	}
	return cloned
}

func freezeRepoNames(source IndexConfig) IndexConfig {
	cloned := cloneIndexConfig(source)
	for i := range cloned.Repos {
		cloned.Repos[i].Name = repoConfigName(cloned.Repos[i])
	}
	return cloned
}
