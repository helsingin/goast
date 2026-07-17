package index

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitLocation struct {
	Path      string
	Root      string
	CommonDir string
}

// inspectWorktreeRoot accepts only the top level of a real Git worktree. The
// shared common directory is the stable identity connecting main and linked
// worktrees without granting access to unrelated repositories.
func inspectWorktreeRoot(path string) (WorktreeStatus, error) {
	if !filepath.IsAbs(path) {
		return WorktreeStatus{}, fmt.Errorf("invalid worktree_root %q: path must be absolute", path)
	}
	location, err := inspectGitLocation(path)
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("invalid worktree_root %q: %w", path, err)
	}
	if !samePath(location.Path, location.Root) {
		return WorktreeStatus{}, fmt.Errorf(
			"invalid worktree_root %q: path is inside worktree %q but is not its root",
			path, location.Root,
		)
	}
	registered, err := registeredWorktreeRoots(location.Root)
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("list registered Git worktrees: %w", err)
	}
	found := false
	for _, root := range registered {
		if samePath(root, location.Root) {
			found = true
			break
		}
	}
	if !found {
		return WorktreeStatus{}, fmt.Errorf(
			"invalid worktree_root %q: Git does not list it as a registered worktree",
			path,
		)
	}

	head, err := runGit(location.Root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("read worktree HEAD: %w", err)
	}
	branch, err := runGit(location.Root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			return WorktreeStatus{}, fmt.Errorf("read worktree branch: %w", err)
		}
		branch = "(detached)"
	}

	return WorktreeStatus{
		Root:      location.Root,
		CommonDir: location.CommonDir,
		Branch:    branch,
		Head:      head,
	}, nil
}

func inspectGitLocation(path string) (gitLocation, error) {
	canonicalPath, err := canonicalDirectory(path)
	if err != nil {
		return gitLocation{}, err
	}

	rootText, err := runGit(canonicalPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitLocation{}, fmt.Errorf("resolve Git worktree root: %w", err)
	}
	root, err := canonicalDirectory(rootText)
	if err != nil {
		return gitLocation{}, fmt.Errorf("canonicalize Git worktree root: %w", err)
	}

	commonText, err := runGit(canonicalPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return gitLocation{}, fmt.Errorf("resolve Git common directory: %w", err)
	}
	if !filepath.IsAbs(commonText) {
		commonText = filepath.Join(canonicalPath, commonText)
	}
	commonDir, err := canonicalDirectory(commonText)
	if err != nil {
		return gitLocation{}, fmt.Errorf("canonicalize Git common directory: %w", err)
	}

	return gitLocation{Path: canonicalPath, Root: root, CommonDir: commonDir}, nil
}

// applyWorktreeOverrides maps every configured Go module belonging to an
// overridden Git repository into the corresponding relative directory of the
// selected worktree. This supports monorepos with several configured modules.
func applyWorktreeOverrides(
	configured IndexConfig,
	overrides map[string]WorktreeStatus,
	requiredCommonDir string,
) (IndexConfig, map[string]WorktreeStatus, map[int]string, error) {
	active := cloneIndexConfig(configured)
	if len(overrides) == 0 {
		return active, make(map[string]WorktreeStatus), make(map[int]string), nil
	}

	locations := make([]gitLocation, len(configured.Repos))
	for i, repo := range configured.Repos {
		location, err := inspectGitLocation(repo.Path)
		if err != nil {
			continue
		}
		locations[i] = location
	}

	worktrees := make(map[string]WorktreeStatus, len(overrides))
	refreshed := make(map[string]WorktreeStatus, len(overrides))
	for expectedCommonDir, previous := range overrides {
		worktree, err := inspectWorktreeRoot(previous.Root)
		if err != nil {
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"validate active worktree override %q: %w",
				previous.Root,
				err,
			)
		}
		if !samePath(worktree.CommonDir, expectedCommonDir) {
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"worktree %q changed Git identity from %q to %q",
				previous.Root, expectedCommonDir, worktree.CommonDir,
			)
		}
		worktrees[expectedCommonDir] = worktree
		refreshed[worktree.CommonDir] = worktree
	}

	matched := make(map[string]bool, len(worktrees))
	repositoryOverrides := make(map[int]string)
	for i, repo := range configured.Repos {
		location := locations[i]
		if location.Root == "" {
			continue
		}
		worktree, ok := worktrees[location.CommonDir]
		if !ok {
			continue
		}

		relativeModule, err := filepath.Rel(location.Root, location.Path)
		if err != nil || relativeModule == ".." || strings.HasPrefix(relativeModule, ".."+string(filepath.Separator)) {
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"configured repository %q is outside its Git worktree root %q",
				repo.Path, location.Root,
			)
		}
		activePath, err := canonicalDirectory(filepath.Join(worktree.Root, relativeModule))
		if err != nil {
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"resolve configured module %q in worktree %q: %w",
				repo.Path, worktree.Root, err,
			)
		}
		activeLocation, err := inspectGitLocation(activePath)
		if err != nil {
			return IndexConfig{}, nil, nil, fmt.Errorf("validate active repository %q: %w", activePath, err)
		}
		if !samePath(activeLocation.CommonDir, worktree.CommonDir) || !samePath(activeLocation.Root, worktree.Root) {
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"active repository %q does not belong to selected worktree %q",
				activePath, worktree.Root,
			)
		}
		if _, err := ParseGoMod(filepath.Join(activePath, "go.mod")); err != nil {
			return IndexConfig{}, nil, nil, fmt.Errorf("active repository %q: %w", activePath, err)
		}

		active.Repos[i].Path = activePath
		matched[worktree.CommonDir] = true
		repositoryOverrides[i] = worktree.CommonDir
	}

	for commonDir, worktree := range worktrees {
		if !matched[commonDir] {
			if commonDir == requiredCommonDir {
				return IndexConfig{}, nil, nil, fmt.Errorf(
					"worktree_root %q is unrelated to every configured repository",
					worktree.Root,
				)
			}
			return IndexConfig{}, nil, nil, fmt.Errorf(
				"active worktree override %q no longer matches the configured repositories; pass reset_worktrees to clear it",
				worktree.Root,
			)
		}
	}
	return active, refreshed, repositoryOverrides, nil
}

func validateWorktreeProvenance(overrides map[string]WorktreeStatus) (map[string]WorktreeStatus, error) {
	refreshed := make(map[string]WorktreeStatus, len(overrides))
	for expectedCommonDir, expected := range overrides {
		worktree, err := inspectWorktreeRoot(expected.Root)
		if err != nil {
			return nil, fmt.Errorf("verify worktree provenance for %q after indexing: %w", expected.Root, err)
		}
		if !samePath(worktree.CommonDir, expectedCommonDir) {
			return nil, fmt.Errorf(
				"worktree %q changed Git identity during indexing from %q to %q",
				expected.Root, expectedCommonDir, worktree.CommonDir,
			)
		}
		if !samePath(worktree.Root, expected.Root) || worktree.Branch != expected.Branch || worktree.Head != expected.Head {
			return nil, fmt.Errorf(
				"worktree %q changed branch or HEAD during indexing (before: branch=%q head=%s; after: branch=%q head=%s)",
				expected.Root,
				expected.Branch,
				expected.Head,
				worktree.Branch,
				worktree.Head,
			)
		}
		refreshed[expectedCommonDir] = worktree
	}
	return refreshed, nil
}

func captureRepositoryStatus(
	configured, active IndexConfig,
	overrides map[string]WorktreeStatus,
	repositoryOverrides map[int]string,
) []RepositoryStatus {
	status := make([]RepositoryStatus, len(configured.Repos))
	for i, repo := range configured.Repos {
		activePath := ""
		if i < len(active.Repos) {
			activePath = active.Repos[i].Path
		}
		entry := RepositoryStatus{
			Name:                  repoConfigName(repo),
			ConfiguredPath:        repo.Path,
			ActivePath:            activePath,
			IncludeTests:          repo.IncludeTests,
			TypedMethodReferences: repo.TypedMethodReferences,
		}
		if commonDir, ok := repositoryOverrides[i]; ok {
			if worktree, exists := overrides[commonDir]; exists {
				entry.GitRoot = worktree.Root
				entry.GitCommonDir = worktree.CommonDir
				entry.Branch = worktree.Branch
				entry.Head = worktree.Head
				entry.WorktreeOverride = true
				status[i] = entry
				continue
			}
		}
		location, err := inspectGitLocation(activePath)
		if err == nil {
			worktree, worktreeErr := inspectWorktreeRoot(location.Root)
			if worktreeErr == nil {
				entry.GitRoot = worktree.Root
				entry.GitCommonDir = worktree.CommonDir
				entry.Branch = worktree.Branch
				entry.Head = worktree.Head
			}
		}
		status[i] = entry
	}
	return status
}

func registeredWorktreeRoots(directory string) ([]string, error) {
	output, err := runGitBytes(directory, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("Git must support `git worktree list --porcelain -z`: %w", err)
	}
	var roots []string
	for _, field := range bytes.Split(output, []byte{0}) {
		const prefix = "worktree "
		if !bytes.HasPrefix(field, []byte(prefix)) {
			continue
		}
		root, err := canonicalDirectory(string(field[len(prefix):]))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Clean(resolved), nil
}

func samePath(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func runGit(directory string, args ...string) (string, error) {
	output, err := runGitBytes(directory, args...)
	if err != nil {
		return "", err
	}
	text := strings.TrimSuffix(string(output), "\n")
	text = strings.TrimSuffix(text, "\r")
	if strings.ContainsAny(text, "\r\n") {
		return "", fmt.Errorf("git returned unexpected multiline output")
	}
	return text, nil
}

func runGitBytes(directory string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = gitCommandEnvironment()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

func gitCommandEnvironment() []string {
	blocked := map[string]bool{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_CEILING_DIRECTORIES":          true,
		"GIT_COMMON_DIR":                   true,
		"GIT_DIR":                          true,
		"GIT_DISCOVERY_ACROSS_FILESYSTEM":  true,
		"GIT_INDEX_FILE":                   true,
		"GIT_NAMESPACE":                    true,
		"GIT_OBJECT_DIRECTORY":             true,
		"GIT_PREFIX":                       true,
		"GIT_SHALLOW_FILE":                 true,
		"GIT_WORK_TREE":                    true,
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
