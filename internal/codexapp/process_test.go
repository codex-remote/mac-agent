package codexapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutableFollowsCodexSymlink(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "ChatGPT.app", "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(resources, "codex")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkDirectory := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(linkDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDirectory, "codex")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("resolved path = %q, want %q", resolved, want)
	}
}

func TestValidateAppBundleRequiresExecutableCodeModeHost(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "ChatGPT.app", "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(resources, "codex")

	if err := validateAppBundle(codex); err == nil {
		t.Fatal("validateAppBundle() succeeded without codex-code-mode-host")
	}
	host := filepath.Join(resources, "codex-code-mode-host")
	if err := os.WriteFile(host, []byte("host"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateAppBundle(codex); err == nil {
		t.Fatal("validateAppBundle() succeeded with a non-executable host")
	}
	if err := os.Chmod(host, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateAppBundle(codex); err != nil {
		t.Fatalf("validateAppBundle() error = %v", err)
	}
}

func TestValidateAppBundleIgnoresStandaloneCodexLayout(t *testing.T) {
	if err := validateAppBundle(filepath.Join(t.TempDir(), "bin", "codex")); err != nil {
		t.Fatalf("validateAppBundle() error = %v", err)
	}
}
