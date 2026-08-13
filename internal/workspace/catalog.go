package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrProjectNotFound = errors.New("project not found")

type Project struct {
	ID         string
	Name       string
	Path       string
	RootPaths  []string
	ThreadCWDs map[string]string
}

func (p Project) WorkingDirectory(threadID string) string {
	if cwd := p.ThreadCWDs[threadID]; cwd != "" {
		return cwd
	}
	return p.Path
}

func (p Project) ContainsPath(path string) bool {
	return p.MatchPath(path) >= 0
}

func (p Project) MatchPath(path string) int {
	longest := -1
	roots := p.RootPaths
	if len(roots) == 0 && p.Path != "" {
		roots = []string{p.Path}
	}
	for _, root := range roots {
		if pathWithin(root, path) && len(root) > longest {
			longest = len(root)
		}
	}
	return longest
}

type Catalog struct {
	roots     []string
	stateFile string
}

type desktopState struct {
	LocalProjects            map[string]desktopProject   `json:"local-projects"`
	ProjectOrder             []string                    `json:"project-order"`
	ThreadProjectAssignments map[string]threadAssignment `json:"thread-project-assignments"`
}

type desktopProject struct {
	Name      string   `json:"name"`
	RootPaths []string `json:"rootPaths"`
}

type threadAssignment struct {
	ProjectKind string `json:"projectKind"`
	ProjectID   string `json:"projectId"`
	CWD         string `json:"cwd"`
}

func NewCodexCatalog(roots []string, stateFile string) (*Catalog, error) {
	canonicalRoots, err := canonicalRoots(roots)
	if err != nil {
		return nil, err
	}
	stateFile = strings.TrimSpace(stateFile)
	if stateFile == "" {
		return nil, fmt.Errorf("Codex Desktop state file is required")
	}
	absoluteStateFile, err := filepath.Abs(stateFile)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex Desktop state file: %w", err)
	}
	return &Catalog{roots: canonicalRoots, stateFile: filepath.Clean(absoluteStateFile)}, nil
}

func (c *Catalog) List(ctx context.Context) ([]Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(c.stateFile)
	if err != nil {
		return nil, fmt.Errorf("read Codex Desktop state %q: %w", c.stateFile, err)
	}
	var state desktopState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode Codex Desktop state %q: %w", c.stateFile, err)
	}
	if state.LocalProjects == nil || state.ProjectOrder == nil {
		return nil, fmt.Errorf("Codex Desktop state %q does not contain local-projects and project-order", c.stateFile)
	}

	assignments := make(map[string]map[string]string)
	for threadID, assignment := range state.ThreadProjectAssignments {
		if assignment.ProjectKind != "local" || assignment.ProjectID == "" || assignment.CWD == "" {
			continue
		}
		if assignments[assignment.ProjectID] == nil {
			assignments[assignment.ProjectID] = make(map[string]string)
		}
		assignments[assignment.ProjectID][threadID] = assignment.CWD
	}

	projects := make([]Project, 0, len(state.ProjectOrder))
	seenPaths := make(map[string]struct{}, len(state.ProjectOrder))
	for _, codexProjectID := range state.ProjectOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		metadata, ok := state.LocalProjects[codexProjectID]
		if !ok {
			continue
		}
		rootPaths, canonicalPaths := c.allowedProjectRoots(metadata.RootPaths)
		if len(rootPaths) == 0 {
			continue
		}
		primaryCanonical := canonicalPaths[0]
		if _, duplicate := seenPaths[primaryCanonical]; duplicate {
			continue
		}
		seenPaths[primaryCanonical] = struct{}{}
		name := strings.TrimSpace(metadata.Name)
		if name == "" {
			name = filepath.Base(rootPaths[0])
		}
		project := Project{
			ID:         projectID(primaryCanonical),
			Name:       name,
			Path:       rootPaths[0],
			RootPaths:  rootPaths,
			ThreadCWDs: make(map[string]string),
		}
		for threadID, cwd := range assignments[codexProjectID] {
			logical, canonical, err := existingDirectory(cwd)
			if err != nil || !containsAny(canonicalPaths, canonical) {
				continue
			}
			project.ThreadCWDs[threadID] = logical
		}
		projects = append(projects, project)
	}
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

func (c *Catalog) allowedProjectRoots(values []string) ([]string, []string) {
	logicalPaths := make([]string, 0, len(values))
	canonicalPaths := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		logical, canonical, err := existingDirectory(value)
		if err != nil || !containsAny(c.roots, canonical) {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		logicalPaths = append(logicalPaths, logical)
		canonicalPaths = append(canonicalPaths, canonical)
	}
	return logicalPaths, canonicalPaths
}

type StaticCatalog struct {
	project Project
}

func NewGitProjectCatalog(path string) (*StaticCatalog, error) {
	_, canonical, err := existingDirectory(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(canonical, ".git")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("path is not a Git repository: %s", canonical)
		}
		return nil, fmt.Errorf("inspect Git repository: %w", err)
	}
	project := Project{
		ID:         projectID(canonical),
		Name:       filepath.Base(canonical),
		Path:       canonical,
		RootPaths:  []string{canonical},
		ThreadCWDs: make(map[string]string),
	}
	return &StaticCatalog{project: project}, nil
}

func (c *StaticCatalog) List(ctx context.Context) ([]Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []Project{c.project}, nil
}

func (c *StaticCatalog) Resolve(ctx context.Context, projectID string) (Project, error) {
	if err := ctx.Err(); err != nil {
		return Project{}, err
	}
	if c.project.ID != projectID {
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	return c.project, nil
}

func canonicalRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one workspace root is required")
	}
	values := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		_, canonical, err := existingDirectory(root)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", root, err)
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		values = append(values, canonical)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one workspace root is required")
	}
	return values, nil
}

func existingDirectory(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("directory path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve absolute path: %w", err)
	}
	logical := filepath.Clean(absolute)
	canonical, err := filepath.EvalSymlinks(logical)
	if err != nil {
		return "", "", fmt.Errorf("resolve symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", "", fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("path is not a directory: %s", canonical)
	}
	return logical, canonical, nil
}

func containsAny(roots []string, path string) bool {
	for _, root := range roots {
		if pathWithin(root, path) {
			return true
		}
	}
	return false
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func projectID(path string) string {
	digest := sha256.Sum256([]byte(path))
	return "project_" + hex.EncodeToString(digest[:12])
}
