package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/codex-remote/mac-agent/internal/protocol"
	"github.com/codex-remote/mac-agent/internal/workspace"
)

const (
	maxSourceBytes = 1024 * 1024
	// encoding/json can expand source characters such as '<' to six bytes.
	// Keep the raw window small enough for Relay's 256 KiB WebSocket frame.
	maxResponseBytes = 32 * 1024
	defaultContext   = 200
	maxContext       = 500
)

type Catalog interface {
	Resolve(context.Context, string) (workspace.Project, error)
}

type Reader struct{ catalog Catalog }

type ReadError struct {
	code    string
	message string
}

func (e *ReadError) Error() string         { return e.code }
func (e *ReadError) SourceCode() string    { return e.code }
func (e *ReadError) PublicMessage() string { return e.message }

func New(catalog Catalog) *Reader { return &Reader{catalog: catalog} }

func (r *Reader) Read(ctx context.Context, request protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error) {
	if strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.Path) == "" || strings.ContainsRune(request.Path, '\x00') {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_INVALID", "A project and source path are required.")
	}
	project, err := r.catalog.Resolve(ctx, request.ProjectID)
	if err != nil {
		return protocol.SourceSnapshotPayload{}, readError("PROJECT_NOT_FOUND", "The selected project is unavailable.")
	}
	resolved, relative, err := resolveProjectFile(project, request.Path)
	if err != nil {
		return protocol.SourceSnapshotPayload{}, err
	}
	if sensitivePath(relative) {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_FORBIDDEN", "This file is not available in the source viewer.")
	}
	info, err := os.Stat(resolved)
	if errors.Is(err, fs.ErrNotExist) {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_NOT_FOUND", "The referenced file no longer exists.")
	}
	if err != nil || !info.Mode().IsRegular() {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_INVALID", "The reference is not a readable regular file.")
	}
	if info.Size() > maxSourceBytes {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_TOO_LARGE", "The source file is too large to preview.")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_READ_FAILED", "The source file could not be read.")
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_BINARY", "Binary files cannot be shown in the source viewer.")
	}

	lines := splitLines(string(data))
	focus := request.FocusLine
	if focus < 1 && len(lines) > 0 {
		focus = 1
	}
	if focus > len(lines) {
		focus = len(lines)
	}
	contextLines := request.ContextLines
	if contextLines < 1 {
		contextLines = defaultContext
	}
	if contextLines > maxContext {
		contextLines = maxContext
	}
	start, end := sourceWindow(lines, focus, contextLines)
	content := strings.Join(lines[start-1:end], "\n")
	for len(content) > maxResponseBytes && end > start {
		if focus-start > end-focus {
			start++
		} else {
			end--
		}
		content = strings.Join(lines[start-1:end], "\n")
	}
	if len(content) > maxResponseBytes {
		return protocol.SourceSnapshotPayload{}, readError("SOURCE_TOO_LARGE", "The selected source line is too large to preview.")
	}
	digest := sha256.Sum256(data)
	return protocol.SourceSnapshotPayload{
		ProjectID: request.ProjectID, Path: relative, Content: content,
		StartLine: start, EndLine: end, TotalLines: len(lines), FocusLine: focus,
		Truncated: start > 1 || end < len(lines), SHA256: hex.EncodeToString(digest[:]), ModifiedAt: info.ModTime().UTC(),
	}, nil
}

func resolveProjectFile(project workspace.Project, requested string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	roots := project.RootPaths
	if len(roots) == 0 && project.Path != "" {
		roots = []string{project.Path}
	}
	if !filepath.IsAbs(requested) {
		clean := filepath.Clean(filepath.FromSlash(requested))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", "", readError("SOURCE_FORBIDDEN", "The source path is outside the selected project.")
		}
		for _, root := range roots {
			if resolved, relative, ok := resolveWithinRoot(root, filepath.Join(root, clean)); ok {
				return resolved, relative, nil
			}
		}
		return "", "", readError("SOURCE_NOT_FOUND", "The referenced file no longer exists.")
	}
	for _, root := range roots {
		if resolved, relative, ok := resolveWithinRoot(root, requested); ok {
			return resolved, relative, nil
		}
	}
	return "", "", readError("SOURCE_FORBIDDEN", "The source path is outside the selected project.")
}

func resolveWithinRoot(root, candidate string) (string, string, bool) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	canonicalCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil || !pathWithin(canonicalRoot, canonicalCandidate) {
		return "", "", false
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	if err != nil || relative == "." {
		return "", "", false
	}
	return canonicalCandidate, filepath.ToSlash(relative), true
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sensitivePath(path string) bool {
	parts := strings.Split(strings.ToLower(filepath.ToSlash(path)), "/")
	for _, part := range parts {
		if part == ".git" || part == ".ssh" || part == ".aws" || part == ".gnupg" {
			return true
		}
	}
	base := parts[len(parts)-1]
	return base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" ||
		base == "credentials" || base == "credentials.json" || base == "id_rsa" || base == "id_ed25519" ||
		strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".p12") ||
		strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".mobileprovision")
}

func splitLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func sourceWindow(lines []string, focus, contextLines int) (int, int) {
	if len(strings.Join(lines, "\n")) <= maxResponseBytes {
		return 1, len(lines)
	}
	start := focus - contextLines
	if start < 1 {
		start = 1
	}
	end := focus + contextLines
	if end > len(lines) {
		end = len(lines)
	}
	return start, end
}

func readError(code, message string) error { return &ReadError{code: code, message: message} }
