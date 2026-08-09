package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultMaxDepth = 4

var ErrProjectNotFound = errors.New("project not found")

type Project struct {
	ID   string
	Name string
	Path string
}

type Catalog struct {
	roots    []string
	maxDepth int
}

func NewCatalog(roots []string, maxDepth int) (*Catalog, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one workspace root is required")
	}
	if maxDepth < 1 {
		maxDepth = DefaultMaxDepth
	}
	canonicalRoots := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		canonical, err := canonicalDirectory(root)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", root, err)
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		canonicalRoots = append(canonicalRoots, canonical)
	}
	if len(canonicalRoots) == 0 {
		return nil, fmt.Errorf("at least one workspace root is required")
	}
	sort.Strings(canonicalRoots)
	return &Catalog{roots: canonicalRoots, maxDepth: maxDepth}, nil
}

func (c *Catalog) List(ctx context.Context) ([]Project, error) {
	projectsByPath := make(map[string]Project)
	for _, root := range c.roots {
		if err := c.scanRoot(ctx, root, projectsByPath); err != nil {
			return nil, err
		}
	}
	projects := make([]Project, 0, len(projectsByPath))
	for _, project := range projectsByPath {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Name == projects[j].Name {
			return projects[i].Path < projects[j].Path
		}
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	return projects, nil
}

func (c *Catalog) Resolve(ctx context.Context, projectID string) (Project, error) {
	projects, err := c.List(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, project := range projects {
		if project.ID == projectID {
			return project, nil
		}
	}
	return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
}

func (c *Catalog) scanRoot(ctx context.Context, root string, projects map[string]Project) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		depth, err := relativeDepth(root, path)
		if err != nil {
			return err
		}
		if depth > c.maxDepth {
			return filepath.SkipDir
		}
		if path != root && shouldSkipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			canonical, err := canonicalDirectory(path)
			if err != nil {
				return err
			}
			projects[canonical] = Project{ID: projectID(canonical), Name: filepath.Base(canonical), Path: canonical}
			return filepath.SkipDir
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", canonical)
	}
	return canonical, nil
}

func relativeDepth(root, path string) (int, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return 0, err
	}
	if relative == "." {
		return 0, nil
	}
	return len(strings.Split(relative, string(filepath.Separator))), nil
}

func shouldSkipDirectory(name string) bool {
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

func projectID(path string) string {
	digest := sha256.Sum256([]byte(path))
	return "project_" + hex.EncodeToString(digest[:12])
}
