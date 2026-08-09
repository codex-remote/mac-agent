package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Resolver interface {
	Resolve(context.Context) (string, error)
}

type Fixed struct {
	path string
}

func NewFixed(path string) (*Fixed, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("stat working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("working directory is not a directory: %s", canonical)
	}
	return &Fixed{path: canonical}, nil
}

func (f *Fixed) Resolve(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "git", "-C", f.path, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("working directory must be inside a Git repository: %w", err)
	}
	root := strings.TrimSpace(string(output))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve Git root: %w", err)
	}
	return canonicalRoot, nil
}
