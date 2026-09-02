package index

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type SourceSnapshot struct {
	Repository              string `json:"repository"`
	BaseCommit              string `json:"base_commit"`
	HeadCommit              string `json:"head_commit"`
	Branch                  string `json:"branch"`
	TrackedDiffDigest       string `json:"tracked_diff_digest"`
	UntrackedManifestDigest string `json:"untracked_manifest_digest"`
	WorktreeDigest          string `json:"worktree_digest"`
	Generation              uint64 `json:"goast_generation"`
	StructuralProvider      string `json:"structural_provider"`
	StructuralGeneration    string `json:"structural_generation"`
	ToolchainIdentity       string `json:"toolchain_identity"`
}

type sourceIdentity struct {
	HeadCommit              string
	Branch                  string
	TrackedDiffDigest       string
	UntrackedManifestDigest string
	WorktreeDigest          string
	ToolchainIdentity       string
}

func captureSourceSnapshot(root, repository, base string, generation uint64) (SourceSnapshot, error) {
	identity, err := captureCurrentSourceIdentity(root)
	if err != nil {
		return SourceSnapshot{}, err
	}
	baseCommit, err := runGit(root, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("resolve base revision %q: %w", base, err)
	}
	providerGeneration := structuralProviderGeneration(generation, identity.ToolchainIdentity, identity.WorktreeDigest)
	return SourceSnapshot{
		Repository: repository, BaseCommit: baseCommit, HeadCommit: identity.HeadCommit,
		Branch: identity.Branch, TrackedDiffDigest: identity.TrackedDiffDigest,
		UntrackedManifestDigest: identity.UntrackedManifestDigest,
		WorktreeDigest:          identity.WorktreeDigest, Generation: generation,
		StructuralProvider: "goast", StructuralGeneration: providerGeneration,
		ToolchainIdentity: identity.ToolchainIdentity,
	}, nil
}

func structuralProviderGeneration(generation uint64, toolchainIdentity, worktreeDigest string) string {
	return digestParts("goast-structural-provider/v1", fmt.Sprint(generation), toolchainIdentity, worktreeDigest)
}

func captureCurrentSourceIdentity(root string) (sourceIdentity, error) {
	head, err := runGit(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	branch, err := runGit(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch = "(detached)"
	}
	trackedDiff, err := runGitBytes(root, "diff", "--binary", "--no-ext-diff", "HEAD", "--")
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("read tracked worktree diff: %w", err)
	}
	trackedDigest := digestBytes(trackedDiff)
	untrackedDigest, err := digestUntrackedManifest(root)
	if err != nil {
		return sourceIdentity{}, err
	}
	toolchain := runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
	combined := digestParts(head, trackedDigest, untrackedDigest)
	return sourceIdentity{
		HeadCommit: head, Branch: branch, TrackedDiffDigest: trackedDigest,
		UntrackedManifestDigest: untrackedDigest, WorktreeDigest: combined,
		ToolchainIdentity: toolchain,
	}, nil
}

func digestUntrackedManifest(root string) (string, error) {
	output, err := runGitBytes(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", fmt.Errorf("list untracked files: %w", err)
	}
	paths := splitNUL(output)
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("inspect untracked file %q: %w", relative, err)
		}
		writeHashField(hash, relative)
		writeHashField(hash, info.Mode().String())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", fmt.Errorf("read untracked symlink %q: %w", relative, err)
			}
			writeHashField(hash, target)
			continue
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("untracked path %q is not a regular file or symlink", relative)
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open untracked file %q: %w", relative, err)
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash untracked file %q: %w", relative, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close untracked file %q: %w", relative, closeErr)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func writeHashField(writer io.Writer, value string) {
	_, _ = io.WriteString(writer, fmt.Sprintf("%d:", len(value)))
	_, _ = io.WriteString(writer, value)
}

func digestBytes(value []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(value))
}

func digestParts(values ...string) string {
	var buffer bytes.Buffer
	for _, value := range values {
		writeHashField(&buffer, value)
	}
	return digestBytes(buffer.Bytes())
}

func splitNUL(value []byte) []string {
	parts := strings.Split(string(value), "\x00")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
