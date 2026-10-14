package index

import (
	"crypto/sha256"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Capture the files BuildIndex can consume, including ignored/untracked Go
// files. Git's diff alone misses these, and size/mtime can survive a body edit.
// External typed imports use go/build and export archives;
// include their search trees so both changed and newly resolvable imports
// invalidate reuse. Missing/unreadable inputs disable reuse conservatively.
func captureIndexInputs(cfg IndexConfig) (string, error) {
	hash := sha256.New()
	writeHashField(hash, runtime.Version())
	writeHashField(hash, fmt.Sprintf("%#v", build.Default))
	env := os.Environ()
	sort.Strings(env)
	for _, value := range env {
		writeHashField(hash, value)
	}
	typed := false
	for _, repo := range cfg.Repos {
		typed = typed || repo.TypedMethodReferences
	}
	for _, repo := range cfg.Repos {
		digest, err := digestIndexTree(repo.Path, true, func(path string, info fs.FileInfo) bool {
			if info.IsDir() {
				return info.Name() == ".git" || (!typed && (info.Name() == "vendor" || info.Name() == "testdata"))
			}
			if typed {
				return !isTypeInput(path)
			}
			return path != filepath.Join(repo.Path, "go.mod") && (!strings.HasSuffix(path, ".go") || (!repo.IncludeTests && strings.HasSuffix(path, "_test.go")) || shouldExclude(path, repo.Path, cfg.ExcludePatterns))
		})
		if err != nil {
			return "", err
		}
		writeHashField(hash, digest)
	}
	if typed {
		modules, err := digestImportModules()
		if err != nil {
			return "", err
		}
		writeHashField(hash, modules)
		roots := []string{build.Default.GOROOT}
		roots = append(roots, filepath.SplitList(build.Default.GOPATH)...)
		for _, root := range roots {
			for _, sub := range []string{"src", "pkg"} {
				tree := filepath.Join(root, sub)
				if _, err := os.Stat(tree); os.IsNotExist(err) {
					writeHashField(hash, tree+":absent")
					continue
				}
				digest, err := digestIndexTree(tree, false, func(path string, info fs.FileInfo) bool {
					if info.IsDir() {
						return info.Name() == ".git" || path == filepath.Join(root, "pkg", "mod") || path == filepath.Join(root, "pkg", "sumdb")
					}
					return !isTypeInput(path)
				})
				if err != nil {
					return "", err
				}
				writeHashField(hash, digest)
			}
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func isTypeInput(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".a") || strings.HasSuffix(path, ".o") || base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum"
}

func digestIndexTree(root string, indexWalk bool, skip func(string, fs.FileInfo) bool) (string, error) {
	hash := sha256.New()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	writeHashField(hash, canonical)
	err = filepath.Walk(canonical, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(canonical, path)
		if err != nil {
			return err
		}
		logical := filepath.Join(root, relative)
		// Directory symlinks can affect external import resolution but Walk
		// does not visit their targets. Decline reuse rather than miss changes.
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Stat(path); err == nil && target.IsDir() {
				if indexWalk {
					return nil
				} // BuildIndex does not traverse directory symlinks.
				return fmt.Errorf("cannot validate directory symlink %s", path)
			}
		}
		if skip(logical, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		writeHashField(hash, path)
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			writeHashField(hash, target)
			resolved, err := os.Stat(path)
			if err != nil {
				return err
			}
			if !resolved.Mode().IsRegular() {
				return fmt.Errorf("cannot validate input %s", path)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot validate input %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeHashField(hash, string(data))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
