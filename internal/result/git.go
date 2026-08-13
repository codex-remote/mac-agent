package result

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultMaxDiffBytes = 128 * 1024

type GitResult struct {
	ChangedFiles  []string
	Diff          string
	DiffTruncated bool
}

type Collector struct {
	MaxDiffBytes int
}

func (c Collector) Collect(ctx context.Context, workingDir string) (GitResult, error) {
	return CollectGit(ctx, workingDir, c.MaxDiffBytes)
}

func CollectGit(ctx context.Context, workingDir string, maxDiffBytes int) (GitResult, error) {
	if maxDiffBytes < 1 {
		maxDiffBytes = DefaultMaxDiffBytes
	}
	repositories, err := discoverGitRepositories(ctx, workingDir)
	if err != nil {
		return GitResult{}, err
	}
	if len(repositories) == 0 {
		return GitResult{}, nil
	}
	var files []string
	var combinedDiff bytes.Buffer
	for _, repository := range repositories {
		prefix := repositoryPrefix(workingDir, repository)
		status, err := gitOutput(ctx, repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err != nil {
			return GitResult{}, fmt.Errorf("collect changed files in %s: %w", repository, err)
		}
		for _, file := range parsePorcelainZ(status) {
			files = append(files, prefix+file)
		}
		diffArgs := []string{"diff", "--no-ext-diff", "--binary"}
		if prefix != "" {
			diffArgs = append(diffArgs, "--src-prefix=a/"+prefix, "--dst-prefix=b/"+prefix)
		}
		diff, err := gitOutput(ctx, repository, append(diffArgs, "--")...)
		if err != nil {
			return GitResult{}, fmt.Errorf("collect Git diff in %s: %w", repository, err)
		}
		combinedDiff.Write(diff)
	}
	sort.Strings(files)
	diff := combinedDiff.Bytes()
	truncated := len(diff) > maxDiffBytes
	if truncated {
		diff = diff[:maxDiffBytes]
	}
	return GitResult{
		ChangedFiles:  files,
		Diff:          strings.ToValidUTF8(string(diff), "�"),
		DiffTruncated: truncated,
	}, nil
}

func discoverGitRepositories(ctx context.Context, workingDir string) ([]string, error) {
	root, err := gitOutput(ctx, workingDir, "rev-parse", "--show-toplevel")
	if err == nil {
		return []string{strings.TrimSpace(string(root))}, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var executableError *exec.Error
	if errors.As(err, &executableError) {
		return nil, err
	}

	var repositories []string
	err = filepath.WalkDir(workingDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != workingDir && skipDiscoveryDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			repositories = append(repositories, path)
			return filepath.SkipDir
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover Git repositories: %w", err)
	}
	sort.Strings(repositories)
	return repositories, nil
}

func repositoryPrefix(workingDir, repository string) string {
	relative, err := filepath.Rel(workingDir, repository)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative) + "/"
}

func skipDiscoveryDirectory(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "DerivedData", "build", "dist":
		return true
	default:
		return false
	}
}

func gitOutput(ctx context.Context, workingDir string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", workingDir}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), bytes.TrimSpace(exitErr.Stderr))
	}
	return nil, err
}

func parsePorcelainZ(data []byte) []string {
	entries := bytes.Split(data, []byte{0})
	files := make([]string, 0, len(entries))
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		status := string(entry[:2])
		files = append(files, string(entry[3:]))
		if strings.ContainsAny(status, "RC") && index+1 < len(entries) {
			index++
		}
	}
	sort.Strings(files)
	return files
}
