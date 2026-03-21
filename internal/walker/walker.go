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
	TreeFileDepth  int // max depth at which files are shown in the tree (0 = root only)
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

	// Cache of gitignore matchers keyed by relative directory path from root.
	// "" = root .gitignore, "subdir" = subdir/.gitignore, etc.
	giCache := make(map[string]*gitignore.GitIgnore)
	if gi := loadGitignore(rootPath); gi != nil {
		giCache[""] = gi
	}

	var customIgnore *gitignore.GitIgnore
	if len(opts.IgnorePatterns) > 0 {
		customIgnore = gitignore.CompileIgnoreLines(opts.IgnorePatterns...)
	}

	var files []string
	fileSizes := make(map[string]int64)
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

		// Load nested .gitignore when entering a directory
		if d.IsDir() {
			if gi := loadGitignore(filepath.Join(rootPath, relPath)); gi != nil {
				giCache[relPath] = gi
			}
		}

		// Check all applicable .gitignore files (from root down to parent directory)
		if matchesAnyGitignore(giCache, relPath, d.IsDir()) {
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
		fileSizes[relPath] = fi.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	sort.Strings(files)

	return &Result{
		Files: files,
		Tree:  buildTree(files, fileSizes, opts.TreeFileDepth),
	}, nil
}

// matchesAnyGitignore checks a path against all applicable .gitignore files
// from root down to the path's parent directory.
func matchesAnyGitignore(giCache map[string]*gitignore.GitIgnore, relPath string, isDir bool) bool {
	// Collect all ancestor directories (including root "")
	dirs := []string{""}
	parts := strings.Split(filepath.Dir(relPath), string(filepath.Separator))
	if parts[0] != "." {
		accumulated := ""
		for _, p := range parts {
			if accumulated == "" {
				accumulated = p
			} else {
				accumulated = accumulated + string(filepath.Separator) + p
			}
			dirs = append(dirs, accumulated)
		}
	}

	for _, dir := range dirs {
		gi, ok := giCache[dir]
		if !ok {
			continue
		}
		// Path checked must be relative to the directory containing the .gitignore
		var checkPath string
		if dir == "" {
			checkPath = relPath
		} else {
			checkPath, _ = filepath.Rel(dir, relPath)
		}
		if matchesIgnore(gi, checkPath, isDir) {
			return true
		}
	}
	return false
}

// matchesIgnore checks if a path matches a gitignore pattern set.
func matchesIgnore(gi *gitignore.GitIgnore, relPath string, isDir bool) bool {
	if isDir {
		return gi.MatchesPath(relPath + "/")
	}
	return gi.MatchesPath(relPath)
}

// loadGitignore parses .gitignore from the given directory. Returns nil if not found.
func loadGitignore(dir string) *gitignore.GitIgnore {
	path := filepath.Join(dir, ".gitignore")
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
	name      string
	children  map[string]*treeNode
	order     []string           // maintains sorted insertion order
	isFile    bool               // true for file leaf nodes
	totalSize int64              // recursive total size in bytes
	extCounts map[string]int     // recursive file counts by extension
}

// buildTree creates a text tree representation from a sorted list of file paths.
// Directories show metadata (file counts by extension, total size).
// Files are shown up to treeFileDepth levels deep; deeper files are omitted.
func buildTree(files []string, fileSizes map[string]int64, treeFileDepth int) string {
	if len(files) == 0 {
		return ".\n"
	}

	root := &treeNode{name: ".", children: make(map[string]*treeNode)}

	for _, f := range files {
		parts := strings.Split(f, string(filepath.Separator))
		current := root
		for i, part := range parts {
			if _, exists := current.children[part]; !exists {
				child := &treeNode{
					name:     part,
					children: make(map[string]*treeNode),
				}
				current.children[part] = child
				current.order = append(current.order, part)
			}
			current = current.children[part]

			// Mark leaf node (file) with size and extension
			if i == len(parts)-1 {
				current.isFile = true
				current.totalSize = fileSizes[f]
				ext := strings.ToLower(filepath.Ext(part))
				if ext == "" {
					ext = part // extensionless files: Makefile, Dockerfile
				}
				current.extCounts = map[string]int{ext: 1}
			}
		}
	}

	computeTreeMeta(root)

	var sb strings.Builder
	sb.WriteString(".\n")
	writeTree(&sb, root, "", 0, treeFileDepth)
	return sb.String()
}

// computeTreeMeta aggregates totalSize and extCounts from leaves to root (post-order).
func computeTreeMeta(n *treeNode) {
	for _, name := range n.order {
		child := n.children[name]
		computeTreeMeta(child)
		n.totalSize += child.totalSize
		for ext, count := range child.extCounts {
			if n.extCounts == nil {
				n.extCounts = make(map[string]int)
			}
			n.extCounts[ext] += count
		}
	}
}

// writeTree recursively writes the tree. Files are shown up to maxFileDepth
// levels deep; at deeper levels, only directories are shown.
func writeTree(sb *strings.Builder, n *treeNode, prefix string, depth int, maxFileDepth int) {
	// Filter visible children: skip files deeper than maxFileDepth
	var visible []string
	for _, name := range n.order {
		child := n.children[name]
		if depth > maxFileDepth && child.isFile {
			continue
		}
		visible = append(visible, name)
	}

	for i, name := range visible {
		child := n.children[name]
		isLast := i == len(visible)-1

		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}

		if child.isFile {
			sb.WriteString(prefix + connector + name + "\n")
		} else {
			sb.WriteString(prefix + connector + name + " " + formatDirMeta(child) + "\n")
			writeTree(sb, child, prefix+childPrefix, depth+1, maxFileDepth)
		}
	}
}

// formatDirMeta formats directory metadata as "(3 .go, 2 .yaml, 1.2 KB)".
func formatDirMeta(n *treeNode) string {
	if len(n.extCounts) == 0 {
		return "(empty)"
	}

	// Sort extensions by count descending, then alphabetically
	type extEntry struct {
		ext   string
		count int
	}
	entries := make([]extEntry, 0, len(n.extCounts))
	for ext, count := range n.extCounts {
		entries = append(entries, extEntry{ext, count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].ext < entries[j].ext
	})

	var parts []string
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%d %s", e.count, e.ext))
	}

	return "(" + strings.Join(parts, ", ") + ", " + formatSize(n.totalSize) + ")"
}

// formatSize formats bytes into a human-readable string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
