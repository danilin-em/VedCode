package walker

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// Options configures the file system walker.
type Options struct {
	RootPath       string
	MaxFileSize    int64
	IgnorePatterns []string
}

// Result contains the output of walking the file system.
type Result struct {
	Files []string // relative file paths
	Tree  string   // text tree representation
}

// binaryExtensions is a set of file extensions considered binary.
var binaryExtensions = map[string]bool{
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".ico": true, ".webp": true, ".tiff": true, ".tif": true,
	// Audio
	".mp3": true, ".wav": true, ".ogg": true, ".flac": true, ".aac": true,
	// Video
	".mp4": true, ".avi": true, ".mov": true, ".mkv": true,
	".wmv": true, ".flv": true, ".webm": true,
	// Archives
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".xz": true, ".rar": true, ".7z": true, ".tgz": true,
	// Binaries/executables
	".exe": true, ".bin": true, ".dll": true, ".so": true,
	".dylib": true, ".o": true, ".a": true, ".class": true,
	".pyc": true, ".pyo": true,
	// Documents (binary formats)
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true,
	// Fonts
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	// Databases
	".db": true, ".sqlite": true, ".sqlite3": true,
	// Other
	".jar": true, ".war": true, ".ear": true,
	".deb": true, ".rpm": true,
	".iso": true, ".dmg": true,
}

// alwaysIgnoredDirs are directories always excluded from walking.
var alwaysIgnoredDirs = map[string]bool{
	".git":     true,
	".vedcode": true,
}

// Walk traverses the file system starting from opts.RootPath and returns
// a filtered list of files and a text tree representation.
func Walk(opts Options) (*Result, error) {
	rootPath, err := filepath.Abs(opts.RootPath)
	if err != nil {
		return nil, fmt.Errorf("resolving root path: %w", err)
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("accessing root path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path is not a directory: %s", rootPath)
	}

	gi := loadGitignore(rootPath)

	var customIgnore *gitignore.GitIgnore
	if len(opts.IgnorePatterns) > 0 {
		customIgnore = gitignore.CompileIgnoreLines(opts.IgnorePatterns...)
	}

	var files []string
	err = filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip entries with errors
		}

		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return nil
		}

		if relPath == "." {
			return nil
		}

		// Skip always-ignored directories
		if d.IsDir() && alwaysIgnoredDirs[d.Name()] {
			return filepath.SkipDir
		}

		// Check .gitignore
		if gi != nil && matchesIgnore(gi, relPath, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check custom ignore patterns
		if customIgnore != nil && matchesIgnore(customIgnore, relPath, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Filter binary files by extension
		ext := strings.ToLower(filepath.Ext(path))
		if binaryExtensions[ext] {
			return nil
		}

		// Filter by file size
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if opts.MaxFileSize > 0 && fi.Size() > opts.MaxFileSize {
			return nil
		}

		// Filter binary files by content (MIME type)
		if isBinaryContent(path) {
			return nil
		}

		files = append(files, relPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	sort.Strings(files)

	return &Result{
		Files: files,
		Tree:  buildTree(files),
	}, nil
}

// matchesIgnore checks if a path matches a gitignore pattern set.
func matchesIgnore(gi *gitignore.GitIgnore, relPath string, isDir bool) bool {
	if isDir {
		return gi.MatchesPath(relPath + "/")
	}
	return gi.MatchesPath(relPath)
}

// loadGitignore parses .gitignore from the root directory. Returns nil if not found.
func loadGitignore(rootPath string) *gitignore.GitIgnore {
	path := filepath.Join(rootPath, ".gitignore")
	gi, err := gitignore.CompileIgnoreFile(path)
	if err != nil {
		return nil
	}
	return gi
}

// isBinaryContent detects binary files by reading the first 512 bytes
// and checking the MIME type.
func isBinaryContent(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if n == 0 {
		return false
	}

	contentType := http.DetectContentType(buf[:n])

	if strings.HasPrefix(contentType, "text/") {
		return false
	}

	// Additional text MIME types that don't start with "text/"
	textMIMEPrefixes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-sh",
	}
	for _, prefix := range textMIMEPrefixes {
		if strings.HasPrefix(contentType, prefix) {
			return false
		}
	}

	return true
}

// treeNode represents a node in the file tree structure.
type treeNode struct {
	name     string
	children map[string]*treeNode
	order    []string // maintains sorted insertion order
}

// buildTree creates a text tree representation from a sorted list of file paths.
func buildTree(files []string) string {
	if len(files) == 0 {
		return ".\n"
	}

	root := &treeNode{name: ".", children: make(map[string]*treeNode)}

	for _, f := range files {
		parts := strings.Split(f, string(filepath.Separator))
		current := root
		for _, part := range parts {
			if _, exists := current.children[part]; !exists {
				child := &treeNode{
					name:     part,
					children: make(map[string]*treeNode),
				}
				current.children[part] = child
				current.order = append(current.order, part)
			}
			current = current.children[part]
		}
	}

	var sb strings.Builder
	sb.WriteString(".\n")
	writeTree(&sb, root, "")
	return sb.String()
}

func writeTree(sb *strings.Builder, n *treeNode, prefix string) {
	for i, name := range n.order {
		child := n.children[name]
		isLast := i == len(n.order)-1

		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}

		sb.WriteString(prefix + connector + name + "\n")
		if len(child.children) > 0 {
			writeTree(sb, child, prefix+childPrefix)
		}
	}
}
