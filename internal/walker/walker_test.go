package walker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createFile creates a file with the given content inside root.
func createFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWalk_BasicFiles(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "main.go", "package main")
	createFile(t, root, "lib/utils.go", "package lib")
	createFile(t, root, "lib/helper.go", "package lib")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"lib/helper.go", "lib/utils.go", "main.go"}
	if len(result.Files) != len(expected) {
		t.Fatalf("files count = %d, want %d; got %v", len(result.Files), len(expected), result.Files)
	}
	for i, f := range expected {
		if result.Files[i] != f {
			t.Errorf("files[%d] = %q, want %q", i, result.Files[i], f)
		}
	}
}

func TestWalk_GitignoreFiltering(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, ".gitignore", "vendor/\n*.log\nbuild/output.txt\n")
	createFile(t, root, "main.go", "package main")
	createFile(t, root, "vendor/dep.go", "package dep")
	createFile(t, root, "app.log", "log content")
	createFile(t, root, "build/output.txt", "build output")
	createFile(t, root, "build/Makefile", "all: build")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fileSet := make(map[string]bool)
	for _, f := range result.Files {
		fileSet[f] = true
	}

	if !fileSet["main.go"] {
		t.Error("main.go should be included")
	}
	if !fileSet["build/Makefile"] {
		t.Error("build/Makefile should be included")
	}
	if fileSet["vendor/dep.go"] {
		t.Error("vendor/dep.go should be excluded by .gitignore")
	}
	if fileSet["app.log"] {
		t.Error("app.log should be excluded by .gitignore")
	}
	if fileSet["build/output.txt"] {
		t.Error("build/output.txt should be excluded by .gitignore")
	}
}

func TestWalk_BinaryExtensionFiltering(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "main.go", "package main")
	createFile(t, root, "image.png", "fake png")
	createFile(t, root, "archive.zip", "fake zip")
	createFile(t, root, "app.exe", "fake exe")
	createFile(t, root, "doc.pdf", "fake pdf")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1; got %v", len(result.Files), result.Files)
	}
	if result.Files[0] != "main.go" {
		t.Errorf("files[0] = %q, want %q", result.Files[0], "main.go")
	}
}

func TestWalk_FileSizeFiltering(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "small.go", "package small")
	createFile(t, root, "large.go", strings.Repeat("x", 1024))

	result, err := Walk(Options{
		RootPath:    root,
		MaxFileSize: 100, // 100 bytes
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1; got %v", len(result.Files), result.Files)
	}
	if result.Files[0] != "small.go" {
		t.Errorf("files[0] = %q, want %q", result.Files[0], "small.go")
	}
}

func TestWalk_CustomIgnorePatterns(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "main.go", "package main")
	createFile(t, root, "bundle.min.js", "minified js")
	createFile(t, root, "app.map", "source map")
	createFile(t, root, "lib/code.go", "package lib")

	result, err := Walk(Options{
		RootPath:       root,
		IgnorePatterns: []string{"*.min.js", "*.map"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fileSet := make(map[string]bool)
	for _, f := range result.Files {
		fileSet[f] = true
	}

	if !fileSet["main.go"] {
		t.Error("main.go should be included")
	}
	if !fileSet["lib/code.go"] {
		t.Error("lib/code.go should be included")
	}
	if fileSet["bundle.min.js"] {
		t.Error("bundle.min.js should be excluded by ignore_patterns")
	}
	if fileSet["app.map"] {
		t.Error("app.map should be excluded by ignore_patterns")
	}
}

func TestWalk_AlwaysIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "main.go", "package main")
	createFile(t, root, ".git/config", "git config")
	createFile(t, root, ".git/HEAD", "ref: refs/heads/main")
	createFile(t, root, ".vedcode/project_overview.md", "overview")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1; got %v", len(result.Files), result.Files)
	}
	if result.Files[0] != "main.go" {
		t.Errorf("files[0] = %q, want %q", result.Files[0], "main.go")
	}
}

func TestWalk_BinaryContentFiltering(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "text.txt", "hello world")

	// Create a file with binary content (ELF header)
	binaryContent := []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binaryPath := filepath.Join(root, "binary.dat2")
	if err := os.WriteFile(binaryPath, binaryContent, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1; got %v", len(result.Files), result.Files)
	}
	if result.Files[0] != "text.txt" {
		t.Errorf("files[0] = %q, want %q", result.Files[0], "text.txt")
	}
}

func TestWalk_EmptyDirectory(t *testing.T) {
	root := t.TempDir()

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 0 {
		t.Errorf("files count = %d, want 0", len(result.Files))
	}
	if result.Tree != ".\n" {
		t.Errorf("tree = %q, want %q", result.Tree, ".\n")
	}
}

func TestWalk_InvalidRootPath(t *testing.T) {
	_, err := Walk(Options{RootPath: "/nonexistent/path"})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestWalk_RootPathIsFile(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Walk(Options{RootPath: filePath})
	if err == nil {
		t.Fatal("expected error when root path is a file")
	}
}

func TestWalk_TreeFormat(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "cmd/indexer/main.go", "package main")
	createFile(t, root, "cmd/mcp/main.go", "package main")
	createFile(t, root, "go.mod", "module test")
	createFile(t, root, "internal/config/config.go", "package config")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedLines := []string{
		".",
		"├── cmd",
		"│   ├── indexer",
		"│   │   └── main.go",
		"│   └── mcp",
		"│       └── main.go",
		"├── go.mod",
		"└── internal",
		"    └── config",
		"        └── config.go",
	}
	expected := strings.Join(expectedLines, "\n") + "\n"

	if result.Tree != expected {
		t.Errorf("tree mismatch:\ngot:\n%s\nwant:\n%s", result.Tree, expected)
	}
}

func TestWalk_NoGitignoreFile(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "main.go", "package main")
	// No .gitignore file — should still work without errors

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1", len(result.Files))
	}
}

func TestWalk_EmptyFile(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, "empty.go", "")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files count = %d, want 1; got %v", len(result.Files), result.Files)
	}
	if result.Files[0] != "empty.go" {
		t.Errorf("files[0] = %q, want %q", result.Files[0], "empty.go")
	}
}

func TestWalk_GitignoreDirectoryPattern(t *testing.T) {
	root := t.TempDir()
	createFile(t, root, ".gitignore", "node_modules/\n")
	createFile(t, root, "index.js", "const x = 1")
	createFile(t, root, "node_modules/pkg/index.js", "module.exports = {}")

	result, err := Walk(Options{RootPath: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, f := range result.Files {
		if strings.HasPrefix(f, "node_modules") {
			t.Errorf("node_modules file should be excluded: %s", f)
		}
	}
}
