package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"VedCode/internal/config"
	"VedCode/internal/prompts"
	"VedCode/internal/providers"
	"VedCode/internal/store"
	"VedCode/internal/walker"
)

// fileAnalysis represents the JSON response from the LLM for source code analysis.
type fileAnalysis struct {
	Summary          string   `json:"summary"`
	Responsibilities []string `json:"responsibilities"`
	Domain           string   `json:"domain"`
	Language         string   `json:"language"`
}

// fileAnalysisSchema is the JSON schema for structured LLM responses.
const fileAnalysisSchema = `{
	"type": "object",
	"properties": {
		"summary": {"type": "string"},
		"responsibilities": {"type": "array", "items": {"type": "string"}},
		"domain": {"type": "string"},
		"language": {"type": "string"}
	},
	"required": ["summary", "responsibilities", "domain", "language"],
	"propertyOrdering": ["summary", "responsibilities", "domain", "language"]
}`

// dirAnalysis represents the JSON response from the LLM for directory analysis.
type dirAnalysis struct {
	Summary          string   `json:"summary"`
	Responsibilities []string `json:"responsibilities"`
	Domain           string   `json:"domain"`
}

// dirAnalysisSchema is the JSON schema for structured LLM responses for directories.
const dirAnalysisSchema = `{
	"type": "object",
	"properties": {
		"summary": {"type": "string"},
		"responsibilities": {"type": "array", "items": {"type": "string"}},
		"domain": {"type": "string"}
	},
	"required": ["summary", "responsibilities", "domain"],
	"propertyOrdering": ["summary", "responsibilities", "domain"]
}`

// Run executes the full indexing cycle for the project.
func Run(configPath string, force bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rootPath, err := filepath.Abs(cfg.Project.RootPath)
	if err != nil {
		return fmt.Errorf("resolving root path: %w", err)
	}

	// Initialize providers
	llm, err := providers.NewTextGenerator(cfg.LLM)
	if err != nil {
		return fmt.Errorf("creating text generator: %w", err)
	}
	embedder, err := providers.NewEmbeddingProvider(cfg.Embedding)
	if err != nil {
		return fmt.Errorf("creating embedding provider: %w", err)
	}

	// Initialize store
	db := store.NewQdrantStore(cfg.Storage.URL, cfg.Storage.CollectionPrefix, cfg.Project.Name)

	// Force mode: delete existing data and start fresh
	if force {
		log.Println("Force mode: cleaning up existing data...")

		overviewPath := filepath.Join(rootPath, ".vedcode", "project_overview.md")
		if err := os.Remove(overviewPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing project overview: %w", err)
		}
		log.Println("Deleted .vedcode/project_overview.md")

		if err := db.DeleteCollection(); err != nil {
			log.Printf("Warning: could not delete collection: %v", err)
		} else {
			log.Println("Deleted Qdrant collection")
		}
	}

	if err := db.EnsureCollection(); err != nil {
		return fmt.Errorf("ensuring collection: %w", err)
	}

	log.Println("=== VedCode Indexer ===")
	log.Printf("Project: %s", cfg.Project.Name)
	log.Printf("Root: %s", rootPath)

	// --- Stage 1: Project structure analysis & cleanup ---
	log.Println("\n--- Stage 1: Project structure analysis & cleanup ---")

	walkResult, err := walker.Walk(walker.Options{
		RootPath:       rootPath,
		MaxFileSize:    cfg.Indexer.MaxFileSize,
		IgnorePatterns: cfg.Indexer.IgnorePatterns,
	})
	if err != nil {
		return fmt.Errorf("walking project: %w", err)
	}
	log.Printf("Found %d files", len(walkResult.Files))

	// Build a set of current files for fast lookup
	currentFiles := make(map[string]bool, len(walkResult.Files))
	for _, f := range walkResult.Files {
		currentFiles[f] = true
	}

	// Clean up deleted files from Qdrant
	existingPoints, err := db.GetAllFilePoints()
	if err != nil {
		return fmt.Errorf("getting existing points: %w", err)
	}

	var deleteIDs []string
	for _, p := range existingPoints {
		if !currentFiles[p.FilePath] {
			deleteIDs = append(deleteIDs, p.ID)
		}
	}

	deletedCount := 0
	if len(deleteIDs) > 0 {
		if err := db.DeletePoints(deleteIDs); err != nil {
			log.Printf("Warning: error deleting stale points: %v", err)
		} else {
			deletedCount = len(deleteIDs)
		}
	}
	log.Printf("Deleted %d stale file records from Qdrant", deletedCount)

	// Clean up deleted directories from Qdrant
	existingDirPoints, err := db.GetAllDirPoints()
	if err != nil {
		return fmt.Errorf("getting existing dir points: %w", err)
	}

	currentDirs := extractUniqueDirs(walkResult.Files)
	var deleteDirIDs []string
	for _, p := range existingDirPoints {
		if !currentDirs[p.FilePath] {
			deleteDirIDs = append(deleteDirIDs, p.ID)
		}
	}

	deletedDirCount := 0
	if len(deleteDirIDs) > 0 {
		if err := db.DeletePoints(deleteDirIDs); err != nil {
			log.Printf("Warning: error deleting stale dir points: %v", err)
		} else {
			deletedDirCount = len(deleteDirIDs)
		}
	}
	log.Printf("Deleted %d stale directory records from Qdrant", deletedDirCount)

	// Analyze project structure via LLM
	structurePrompt := prompts.Render(cfg.Prompts.ProjectStructureAnalysis, map[string]string{
		"CONTENT": walkResult.Tree,
	})

	log.Println("Analyzing project structure...")
	projectOverview, err := llm.GenerateContent(structurePrompt)
	if err != nil {
		return fmt.Errorf("analyzing project structure: %w", err)
	}

	// Save project overview to .vedcode/project_overview.md
	vedcodeDir := filepath.Join(rootPath, ".vedcode")
	if err := os.MkdirAll(vedcodeDir, 0o755); err != nil {
		return fmt.Errorf("creating .vedcode directory: %w", err)
	}
	overviewPath := filepath.Join(vedcodeDir, "project_overview.md")
	if err := os.WriteFile(overviewPath, []byte(projectOverview), 0o644); err != nil {
		return fmt.Errorf("saving project overview: %w", err)
	}
	log.Printf("Project overview saved to %s", overviewPath)

	// Build existing points map for hash comparison (keyed by file_path)
	existingByPath := make(map[string]*store.Point, len(existingPoints))
	for _, p := range existingPoints {
		existingByPath[p.FilePath] = p
	}

	// --- Stage 2: File indexing ---
	log.Println("\n--- Stage 2: File indexing ---")
	log.Printf("Using %d worker(s)", cfg.Indexer.Workers)

	var indexedCount atomic.Int64
	var errorCount atomic.Int64
	skippedCount := 0

	sem := make(chan struct{}, cfg.Indexer.Workers)
	var wg sync.WaitGroup
	totalFiles := len(walkResult.Files)

	for i, relPath := range walkResult.Files {
		absPath := filepath.Join(rootPath, relPath)

		// Read file and compute hash before spawning goroutine (fast, allows early skip)
		content, err := os.ReadFile(absPath)
		if err != nil {
			log.Printf("[%d/%d] Error reading %s: %v", i+1, totalFiles, relPath, err)
			errorCount.Add(1)
			continue
		}

		hash := sha256sum(content)

		// Check if file needs re-indexing
		if existing, ok := existingByPath[relPath]; ok && existing.FileHash == hash {
			skippedCount++
			continue
		}

		// Acquire semaphore slot and launch worker
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, relPath string, content []byte, hash string) {
			defer wg.Done()
			defer func() { <-sem }()

			log.Printf("[%d/%d] Indexing %s", idx+1, totalFiles, relPath)

			// Analyze file via LLM
			filePrompt := prompts.Render(cfg.Prompts.SourceCodeAnalysis, map[string]string{
				"CONTENT":          string(content),
				"PROJECT_OVERVIEW": projectOverview,
			})

			response, err := llm.GenerateJSON(filePrompt, fileAnalysisSchema)
			if err != nil {
				log.Printf("[%d/%d] Error analyzing %s: %v", idx+1, totalFiles, relPath, err)
				errorCount.Add(1)
				return
			}

			analysis, err := parseAnalysis(response)
			if err != nil {
				log.Printf("[%d/%d] Error parsing analysis for %s: %v", idx+1, totalFiles, relPath, err)
				errorCount.Add(1)
				return
			}

			// Get embedding for the summary
			embedding, err := embedder.EmbedContent(analysis.Summary)
			if err != nil {
				log.Printf("[%d/%d] Error embedding %s: %v", idx+1, totalFiles, relPath, err)
				errorCount.Add(1)
				return
			}

			// Upsert point in Qdrant
			point := &store.Point{
				ID:               store.FilePathToID(relPath),
				Vector:           embedding,
				Summary:          analysis.Summary,
				FilePath:         relPath,
				FileHash:         hash,
				Type:             "file",
				Responsibilities: analysis.Responsibilities,
				Domain:           analysis.Domain,
				Language:         analysis.Language,
				IndexedAt:        time.Now(),
			}

			if err := db.UpsertPoint(point); err != nil {
				log.Printf("[%d/%d] Error saving %s: %v", idx+1, totalFiles, relPath, err)
				errorCount.Add(1)
				return
			}

			indexedCount.Add(1)
		}(i, relPath, content, hash)
	}
	wg.Wait()

	// --- Stage 3: Directory analysis ---
	log.Println("\n--- Stage 3: Directory analysis ---")

	dirIndexed, dirSkipped, dirErrors := indexDirectories(
		db, llm, embedder, cfg, projectOverview,
		walkResult.Files, existingDirPoints,
	)

	// --- Summary ---
	log.Println("\n=== Indexing complete ===")
	log.Printf("Total files:   %d", len(walkResult.Files))
	log.Printf("Indexed:       %d", indexedCount.Load())
	log.Printf("Skipped:       %d (unchanged)", skippedCount)
	log.Printf("Deleted:       %d (removed from project)", deletedCount)
	log.Printf("Errors:        %d", errorCount.Load())
	log.Printf("Dirs indexed:  %d", dirIndexed)
	log.Printf("Dirs skipped:  %d (unchanged)", dirSkipped)
	log.Printf("Dirs errors:   %d", dirErrors)

	return nil
}

// sha256sum computes the SHA256 hex digest of data.
func sha256sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// parseAnalysis parses the JSON response from the LLM.
func parseAnalysis(response string) (*fileAnalysis, error) {
	var analysis fileAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if analysis.Summary == "" {
		return nil, fmt.Errorf("empty summary in analysis response")
	}

	return &analysis, nil
}

// parseDirAnalysis parses the JSON response from the LLM for directory analysis.
func parseDirAnalysis(response string) (*dirAnalysis, error) {
	var analysis dirAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if analysis.Summary == "" {
		return nil, fmt.Errorf("empty summary in dir analysis response")
	}

	return &analysis, nil
}

// extractUniqueDirs collects all unique directories from file paths.
// Includes all nesting levels, excluding the root (".").
func extractUniqueDirs(files []string) map[string]bool {
	dirs := make(map[string]bool)
	for _, f := range files {
		dir := filepath.Dir(f)
		for dir != "." && dir != "" {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	return dirs
}

// buildDirOrder returns directories sorted bottom-up:
// deeper directories (more path segments) come first.
func buildDirOrder(dirs map[string]bool) []string {
	order := make([]string, 0, len(dirs))
	for d := range dirs {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool {
		di := strings.Count(order[i], string(filepath.Separator))
		dj := strings.Count(order[j], string(filepath.Separator))
		if di != dj {
			return di > dj
		}
		return order[i] < order[j]
	})
	return order
}

// computeDirHash computes a deterministic hash for a directory based on
// sorted file hashes of direct children and dir hashes of direct subdirectories.
func computeDirHash(dirPath string, filesByDir map[string][]*store.Point, dirHashCache map[string]string) string {
	var parts []string

	if files, ok := filesByDir[dirPath]; ok {
		fileHashes := make([]string, 0, len(files))
		for _, f := range files {
			fileHashes = append(fileHashes, f.FileHash)
		}
		sort.Strings(fileHashes)
		parts = append(parts, fileHashes...)
	}

	for subDir, h := range dirHashCache {
		if filepath.Dir(subDir) == dirPath {
			parts = append(parts, h)
		}
	}
	sort.Strings(parts)

	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}

// buildFilesSummariesText formats file summaries for the LLM prompt.
func buildFilesSummariesText(files []*store.Point) string {
	if len(files) == 0 {
		return "(no files)"
	}
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString("- ")
		sb.WriteString(f.FilePath)
		sb.WriteString(": ")
		sb.WriteString(f.Summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildSubdirsSummariesText formats subdirectory summaries for the LLM prompt.
func buildSubdirsSummariesText(parentDir string, dirSummaryCache map[string]string) string {
	var lines []string
	for dir, summary := range dirSummaryCache {
		if filepath.Dir(dir) == parentDir {
			lines = append(lines, "- "+dir+": "+summary)
		}
	}
	if len(lines) == 0 {
		return "(no subdirectories)"
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// indexDirectories performs Stage 3: bottom-up directory analysis.
func indexDirectories(
	db store.Store,
	llm providers.TextGenerator,
	embedder providers.EmbeddingProvider,
	cfg *config.Config,
	projectOverview string,
	files []string,
	existingDirPoints []*store.Point,
) (indexed, skipped, errors int) {
	// Load current file points from Qdrant (need their summaries and hashes)
	allFilePoints, err := db.GetAllFilePoints()
	if err != nil {
		log.Printf("Stage 3: error loading file points: %v", err)
		return 0, 0, 1
	}

	// Index file points by directory: dirPath → []Point (direct children only)
	filesByDir := make(map[string][]*store.Point)
	for _, p := range allFilePoints {
		dir := filepath.Dir(p.FilePath)
		if dir == "." {
			dir = ""
		}
		filesByDir[dir] = append(filesByDir[dir], p)
	}

	// Build map of existing dir points: dirPath → Point
	existingDirByPath := make(map[string]*store.Point, len(existingDirPoints))
	for _, p := range existingDirPoints {
		existingDirByPath[p.FilePath] = p
	}

	// Collect unique directories and sort bottom-up
	allDirs := extractUniqueDirs(files)
	orderedDirs := buildDirOrder(allDirs)

	log.Printf("Found %d directories to analyze", len(orderedDirs))

	// Cache of directory hashes: filled during bottom-up traversal
	dirHashCache := make(map[string]string)
	// Cache of directory summaries: for passing to parent directory analysis
	dirSummaryCache := make(map[string]string)

	totalDirs := len(orderedDirs)

	for i, dirPath := range orderedDirs {
		newHash := computeDirHash(dirPath, filesByDir, dirHashCache)
		dirHashCache[dirPath] = newHash

		// Check cache
		if existing, ok := existingDirByPath[dirPath]; ok && existing.FileHash == newHash {
			dirSummaryCache[dirPath] = existing.Summary
			skipped++
			continue
		}

		log.Printf("[%d/%d] Analyzing %s", i+1, totalDirs, dirPath)

		filesSummaries := buildFilesSummariesText(filesByDir[dirPath])
		subdirsSummaries := buildSubdirsSummariesText(dirPath, dirSummaryCache)

		dirPrompt := prompts.Render(cfg.Prompts.DirectoryAnalysis, map[string]string{
			"DIR_PATH":          dirPath,
			"PROJECT_OVERVIEW":  projectOverview,
			"FILES_SUMMARIES":   filesSummaries,
			"SUBDIRS_SUMMARIES": subdirsSummaries,
		})

		response, err := llm.GenerateJSON(dirPrompt, dirAnalysisSchema)
		if err != nil {
			log.Printf("[%d/%d] Error analyzing %s: %v", i+1, totalDirs, dirPath, err)
			errors++
			continue
		}

		analysis, err := parseDirAnalysis(response)
		if err != nil {
			log.Printf("[%d/%d] Error parsing analysis for %s: %v", i+1, totalDirs, dirPath, err)
			errors++
			continue
		}

		embedding, err := embedder.EmbedContent(analysis.Summary)
		if err != nil {
			log.Printf("[%d/%d] Error embedding %s: %v", i+1, totalDirs, dirPath, err)
			errors++
			continue
		}

		point := &store.Point{
			ID:               store.FilePathToID("dir:" + dirPath),
			Vector:           embedding,
			Summary:          analysis.Summary,
			FilePath:         dirPath,
			FileHash:         newHash,
			Type:             "directory",
			Responsibilities: analysis.Responsibilities,
			Domain:           analysis.Domain,
			IndexedAt:        time.Now(),
		}

		if err := db.UpsertPoint(point); err != nil {
			log.Printf("[%d/%d] Error saving %s: %v", i+1, totalDirs, dirPath, err)
			errors++
			continue
		}

		dirSummaryCache[dirPath] = analysis.Summary
		indexed++
	}

	return indexed, skipped, errors
}
