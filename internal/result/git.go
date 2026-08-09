package result

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	status, err := gitOutput(ctx, workingDir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return GitResult{}, fmt.Errorf("collect changed files: %w", err)
	}
	diff, err := gitOutput(ctx, workingDir, "diff", "--no-ext-diff", "--binary", "--")
	if err != nil {
		return GitResult{}, fmt.Errorf("collect Git diff: %w", err)
	}
	files := parsePorcelainZ(status)
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
