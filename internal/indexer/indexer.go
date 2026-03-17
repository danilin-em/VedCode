package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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

// fileInfo holds lightweight file data in memory for directory analysis.
type fileInfo struct {
	filePath string
	fileHash string
	summary  string
}

// dirTracker coordinates interleaved file and directory indexing.
// Directory analysis starts automatically when all children (files + subdirs) are ready.
type dirTracker struct {
	mu          sync.Mutex
	pending     map[string]int          // dir → remaining children (files + subdirs)
	fileInfos   map[string][]*fileInfo  // dir → direct child file infos
	childDirs   map[string][]string     // dir → direct child subdirectories
	dirSummary  map[string]string       // dir → summary (filled after analysis)
	dirHash     map[string]string       // dir → computed hash
	allDirs     map[string]bool         // all tracked directories
	existingDir map[string]*store.Point // existing dir points from Qdrant

	db       store.Store
	llm      providers.TextGenerator
	embedder providers.EmbeddingProvider
	cfg      *config.Config
	overview string
	sem      chan struct{}
	wg       *sync.WaitGroup
	logger   *slog.Logger

	// Shared progress counter (files + dirs) and total items count
	progress   *atomic.Int64
	totalItems int

	indexed atomic.Int64
	skipped atomic.Int64
	errors  atomic.Int64
}

// newDirTracker creates a dirTracker with pre-computed dependency counts.
func newDirTracker(
	files []string,
	existingDirPoints []*store.Point,
	db store.Store,
	llm providers.TextGenerator,
	embedder providers.EmbeddingProvider,
	cfg *config.Config,
	overview string,
	sem chan struct{},
	wg *sync.WaitGroup,
	progress *atomic.Int64,
	totalItems int,
	logger *slog.Logger,
) *dirTracker {
	allDirs := extractUniqueDirs(files)

	// Count direct child files per directory
	pending := make(map[string]int, len(allDirs))
	for _, f := range files {
		dir := filepath.Dir(f)
		if dir == "." {
			continue // root-level files don't belong to any tracked directory
		}
		pending[dir]++
	}

	// Build childDirs map and count direct child subdirectories
	childDirs := make(map[string][]string, len(allDirs))
	for dir := range allDirs {
		parent := filepath.Dir(dir)
		if parent == "." {
			continue
		}
		if allDirs[parent] {
			childDirs[parent] = append(childDirs[parent], dir)
			pending[parent]++
		}
	}

	// Build existing dir points map
	existingDir := make(map[string]*store.Point, len(existingDirPoints))
	for _, p := range existingDirPoints {
		existingDir[p.FilePath] = p
	}

	return &dirTracker{
		pending:     pending,
		fileInfos:   make(map[string][]*fileInfo),
		childDirs:   childDirs,
		dirSummary:  make(map[string]string),
		dirHash:     make(map[string]string),
		allDirs:     allDirs,
		existingDir: existingDir,
		db:          db,
		llm:         llm,
		embedder:    embedder,
		cfg:         cfg,
		overview:    overview,
		sem:         sem,
		wg:          wg,
		progress:    progress,
		totalItems:  totalItems,
		logger:      logger,
	}
}

// fileCompleted is called when a file is indexed or skipped (unchanged).
func (t *dirTracker) fileCompleted(relPath, summary, hash string) {
	dir := filepath.Dir(relPath)
	if dir == "." {
		return // root-level file, no directory to track
	}

	t.mu.Lock()
	t.fileInfos[dir] = append(t.fileInfos[dir], &fileInfo{
		filePath: relPath,
		fileHash: hash,
		summary:  summary,
	})
	t.pending[dir]--
	ready := t.pending[dir] == 0
	t.mu.Unlock()

	if ready {
		t.tryAnalyzeDir(dir)
	}
}

// fileFailed is called when a file fails indexing (graceful degradation).
func (t *dirTracker) fileFailed(relPath string) {
	dir := filepath.Dir(relPath)
	if dir == "." {
		return
	}

	t.mu.Lock()
	t.pending[dir]--
	ready := t.pending[dir] == 0
	t.mu.Unlock()

	if ready {
		t.tryAnalyzeDir(dir)
	}
}

// tryAnalyzeDir checks cache and launches directory analysis when ready.
func (t *dirTracker) tryAnalyzeDir(dirPath string) {
	t.mu.Lock()
	newHash := computeDirHash(dirPath, t.fileInfos, t.childDirs, t.dirHash)
	t.dirHash[dirPath] = newHash

	// Check if directory is unchanged
	if existing, ok := t.existingDir[dirPath]; ok && existing.FileHash == newHash {
		t.dirSummary[dirPath] = existing.Summary
		t.mu.Unlock()

		t.logger.Debug("dir skipped (unchanged)", "dir", dirPath, "hash", newHash)
		t.skipped.Add(1)
		t.notifyParent(dirPath)
		return
	}

	// Collect data for LLM prompt while holding the lock
	filesSummaries := buildFilesSummariesText(t.fileInfos[dirPath])
	subdirsSummaries := buildSubdirsSummariesText(t.childDirs[dirPath], t.dirSummary)
	t.mu.Unlock()

	// Launch analysis in a goroutine
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.sem <- struct{}{}
		defer func() { <-t.sem }()

		n := t.progress.Add(1)
		t.logger.Info(fmt.Sprintf("[%d/%d] Analyzing dir %s", n, t.totalItems, dirPath))
		t.logger.Debug("dir indexing started", "dir", dirPath, "index", n, "total", t.totalItems, "hash", newHash)
		dirStart := time.Now()

		dirPrompt := prompts.Render(t.cfg.Prompts.DirectoryAnalysis, map[string]string{
			"DIR_PATH":          dirPath,
			"PROJECT_OVERVIEW":  t.overview,
			"FILES_SUMMARIES":   filesSummaries,
			"SUBDIRS_SUMMARIES": subdirsSummaries,
		})

		response, err := t.llm.GenerateJSON(dirPrompt, dirAnalysisSchema)
		if err != nil {
			t.logger.Error(fmt.Sprintf("Error analyzing dir %s: %v", dirPath, err))
			t.errors.Add(1)
			t.notifyParent(dirPath)
			return
		}

		analysis, err := parseDirAnalysis(response)
		if err != nil {
			t.logger.Error(fmt.Sprintf("Error parsing dir analysis for %s: %v", dirPath, err))
			t.errors.Add(1)
			t.notifyParent(dirPath)
			return
		}

		embedding, err := t.embedder.EmbedContent(analysis.Summary)
		if err != nil {
			t.logger.Error(fmt.Sprintf("Error embedding dir %s: %v", dirPath, err))
			t.errors.Add(1)
			t.notifyParent(dirPath)
			return
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

		if err := t.db.UpsertPoint(point); err != nil {
			t.logger.Error(fmt.Sprintf("Error saving dir %s: %v", dirPath, err))
			t.errors.Add(1)
			t.notifyParent(dirPath)
			return
		}

		t.logger.Debug("dir indexing completed",
			"dir", dirPath,
			"duration", time.Since(dirStart),
		)

		t.mu.Lock()
		t.dirSummary[dirPath] = analysis.Summary
		t.mu.Unlock()

		t.indexed.Add(1)
		t.notifyParent(dirPath)
	}()
}

// notifyParent signals that a child directory is done.
func (t *dirTracker) notifyParent(dirPath string) {
	parent := filepath.Dir(dirPath)
	if parent == "." || !t.allDirs[parent] {
		return
	}

	t.mu.Lock()
	t.pending[parent]--
	ready := t.pending[parent] == 0
	t.mu.Unlock()

	if ready {
		t.tryAnalyzeDir(parent)
	}
}

// results returns the final directory indexing counters.
func (t *dirTracker) results() (indexed, skipped, errors int) {
	return int(t.indexed.Load()), int(t.skipped.Load()), int(t.errors.Load())
}

// Run executes the full indexing cycle for the project.
func Run(configPath string, force bool, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Debug("config loaded",
		"project", cfg.Project.Name,
		"llm_provider", cfg.LLM.Provider,
		"llm_model", cfg.LLM.Model,
		"embedding_provider", cfg.Embedding.Provider,
		"embedding_model", cfg.Embedding.Model,
		"storage_url", cfg.Storage.URL,
		"workers", cfg.Indexer.Workers,
		"max_file_size", cfg.Indexer.MaxFileSize,
	)

	rootPath, err := filepath.Abs(cfg.Project.RootPath)
	if err != nil {
		return fmt.Errorf("resolving root path: %w", err)
	}

	// Initialize providers
	llm, err := providers.NewTextGenerator(cfg.LLM, logger)
	if err != nil {
		return fmt.Errorf("creating text generator: %w", err)
	}
	embedder, err := providers.NewEmbeddingProvider(cfg.Embedding, logger)
	if err != nil {
		return fmt.Errorf("creating embedding provider: %w", err)
	}

	// Determine vector size: use config value or auto-detect from provider
	vectorSize := cfg.Embedding.VectorSize
	if vectorSize <= 0 {
		vectorSize, err = embedder.DetectVectorSize()
		if err != nil {
			return fmt.Errorf("detecting vector size: %w", err)
		}
		logger.Info("Auto-detected vector size", "vector_size", vectorSize)
	}

	// Initialize store
	db := store.NewQdrantStore(cfg.Storage.URL, cfg.Storage.CollectionPrefix, cfg.Project.Name, vectorSize, logger)

	// Force mode: delete existing data and start fresh
	if force {
		logger.Info("Force mode: cleaning up existing data...")

		overviewPath := filepath.Join(rootPath, ".vedcode", "project_overview.md")
		if err := os.Remove(overviewPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing project overview: %w", err)
		}
		logger.Info("Deleted .vedcode/project_overview.md")

		if err := db.DeleteCollection(); err != nil {
			logger.Warn(fmt.Sprintf("could not delete collection: %v", err))
		} else {
			logger.Info("Deleted Qdrant collection")
		}
	}

	if err := db.EnsureCollection(); err != nil {
		return fmt.Errorf("ensuring collection: %w", err)
	}

	logger.Info("=== VedCode Indexer ===")
	logger.Info(fmt.Sprintf("Project: %s", cfg.Project.Name))
	logger.Info(fmt.Sprintf("Root: %s", rootPath))

	// --- Stage 1: Project structure analysis & cleanup ---
	logger.Info("\n--- Stage 1: Project structure analysis & cleanup ---")

	walkResult, err := walker.Walk(walker.Options{
		RootPath:       rootPath,
		MaxFileSize:    cfg.Indexer.MaxFileSize,
		IgnorePatterns: cfg.Indexer.IgnorePatterns,
	})
	if err != nil {
		return fmt.Errorf("walking project: %w", err)
	}
	logger.Info(fmt.Sprintf("Found %d files", len(walkResult.Files)))
	logger.Debug("walker completed",
		"files_found", len(walkResult.Files),
		"root_path", rootPath,
	)

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
			logger.Warn(fmt.Sprintf("error deleting stale points: %v", err))
		} else {
			deletedCount = len(deleteIDs)
		}
	}
	logger.Info(fmt.Sprintf("Deleted %d stale file records from Qdrant", deletedCount))
	logger.Debug("stale file cleanup", "deleted", deletedCount, "total_existing", len(existingPoints))

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
			logger.Warn(fmt.Sprintf("error deleting stale dir points: %v", err))
		} else {
			deletedDirCount = len(deleteDirIDs)
		}
	}
	logger.Info(fmt.Sprintf("Deleted %d stale directory records from Qdrant", deletedDirCount))
	logger.Debug("stale dir cleanup", "deleted", deletedDirCount, "total_existing", len(existingDirPoints))

	// Analyze project structure via LLM
	structurePrompt := prompts.Render(cfg.Prompts.ProjectStructureAnalysis, map[string]string{
		"CONTENT": walkResult.Tree,
	})

	logger.Info("Analyzing project structure...")
	logger.Debug("analyzing project structure", "prompt_length", len(structurePrompt))

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
	logger.Info(fmt.Sprintf("Project overview saved to %s", overviewPath))

	// Build existing points map for hash comparison (keyed by file_path)
	existingByPath := make(map[string]*store.Point, len(existingPoints))
	for _, p := range existingPoints {
		existingByPath[p.FilePath] = p
	}

	// --- Stage 2: File & directory indexing (interleaved) ---
	logger.Info("\n--- Stage 2: File & directory indexing ---")
	logger.Info(fmt.Sprintf("Using %d worker(s)", cfg.Indexer.Workers))

	var indexedCount atomic.Int64
	var errorCount atomic.Int64
	skippedCount := 0

	sem := make(chan struct{}, cfg.Indexer.Workers)
	var wg sync.WaitGroup
	var progress atomic.Int64

	totalDirs := len(currentDirs)
	totalItems := len(walkResult.Files) + totalDirs

	// Create directory tracker — directories are analyzed automatically
	// as soon as all their children (files + subdirs) are ready.
	tracker := newDirTracker(
		walkResult.Files, existingDirPoints,
		db, llm, embedder, cfg, projectOverview,
		sem, &wg, &progress, totalItems, logger,
	)

	logger.Info(fmt.Sprintf("Found %d items to analyze (%d files, %d dirs)", totalItems, len(walkResult.Files), totalDirs))

	for _, relPath := range walkResult.Files {
		absPath := filepath.Join(rootPath, relPath)

		// Read file and compute hash before spawning goroutine (fast, allows early skip)
		content, err := os.ReadFile(absPath)
		if err != nil {
			n := progress.Add(1)
			logger.Error(fmt.Sprintf("[%d/%d] Error reading %s: %v", n, totalItems, relPath, err))
			errorCount.Add(1)
			tracker.fileFailed(relPath)
			continue
		}

		hash := sha256sum(content)

		// Check if file needs re-indexing
		if existing, ok := existingByPath[relPath]; ok && existing.FileHash == hash {
			logger.Debug("file skipped (unchanged)", "file", relPath, "hash", hash)
			skippedCount++
			tracker.fileCompleted(relPath, existing.Summary, existing.FileHash)
			continue
		}

		// Acquire semaphore slot and launch worker
		sem <- struct{}{}
		wg.Add(1)
		go func(relPath string, content []byte, hash string) {
			defer wg.Done()
			defer func() { <-sem }()

			n := progress.Add(1)
			logger.Info(fmt.Sprintf("[%d/%d] Indexing %s", n, totalItems, relPath))
			logger.Debug("file indexing started",
				"file", relPath,
				"index", n,
				"total", totalItems,
				"hash", hash,
				"size", len(content),
			)
			fileStart := time.Now()

			// Analyze file via LLM
			filePrompt := prompts.Render(cfg.Prompts.SourceCodeAnalysis, map[string]string{
				"CONTENT":          string(content),
				"PROJECT_OVERVIEW": projectOverview,
			})

			response, err := llm.GenerateJSON(filePrompt, fileAnalysisSchema)
			if err != nil {
				logger.Error(fmt.Sprintf("[%d/%d] Error analyzing %s: %v", n, totalItems, relPath, err))
				errorCount.Add(1)
				tracker.fileFailed(relPath)
				return
			}

			analysis, err := parseAnalysis(response)
			if err != nil {
				logger.Error(fmt.Sprintf("[%d/%d] Error parsing analysis for %s: %v", n, totalItems, relPath, err))
				errorCount.Add(1)
				tracker.fileFailed(relPath)
				return
			}

			logger.Debug("file analysis completed",
				"file", relPath,
				"summary_length", len(analysis.Summary),
				"domain", analysis.Domain,
				"language", analysis.Language,
			)

			// Get embedding for the summary
			embedding, err := embedder.EmbedContent(analysis.Summary)
			if err != nil {
				logger.Error(fmt.Sprintf("[%d/%d] Error embedding %s: %v", n, totalItems, relPath, err))
				errorCount.Add(1)
				tracker.fileFailed(relPath)
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
				logger.Error(fmt.Sprintf("[%d/%d] Error saving %s: %v", n, totalItems, relPath, err))
				errorCount.Add(1)
				tracker.fileFailed(relPath)
				return
			}

			logger.Debug("file indexing completed",
				"file", relPath,
				"duration", time.Since(fileStart),
			)

			tracker.fileCompleted(relPath, analysis.Summary, hash)
			indexedCount.Add(1)
		}(relPath, content, hash)
	}
	wg.Wait()

	dirIndexed, dirSkipped, dirErrors := tracker.results()

	// --- Summary ---
	logger.Info("\n=== Indexing complete ===")
	logger.Info(fmt.Sprintf("Total files:   %d", len(walkResult.Files)))
	logger.Info(fmt.Sprintf("Indexed:       %d", indexedCount.Load()))
	logger.Info(fmt.Sprintf("Skipped:       %d (unchanged)", skippedCount))
	logger.Info(fmt.Sprintf("Deleted:       %d (removed from project)", deletedCount))
	logger.Info(fmt.Sprintf("Errors:        %d", errorCount.Load()))
	logger.Info(fmt.Sprintf("Dirs indexed:  %d", dirIndexed))
	logger.Info(fmt.Sprintf("Dirs skipped:  %d (unchanged)", dirSkipped))
	logger.Info(fmt.Sprintf("Dirs errors:   %d", dirErrors))

	logger.Debug("indexing complete",
		"total_files", len(walkResult.Files),
		"indexed", indexedCount.Load(),
		"skipped", skippedCount,
		"deleted", deletedCount,
		"errors", errorCount.Load(),
		"dirs_indexed", dirIndexed,
		"dirs_skipped", dirSkipped,
		"dirs_errors", dirErrors,
	)

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


// computeDirHash computes a deterministic hash for a directory based on
// sorted file hashes of direct children and dir hashes of direct subdirectories.
func computeDirHash(dirPath string, fileInfos map[string][]*fileInfo, childDirs map[string][]string, dirHash map[string]string) string {
	var parts []string

	if files, ok := fileInfos[dirPath]; ok {
		fileHashes := make([]string, 0, len(files))
		for _, f := range files {
			fileHashes = append(fileHashes, f.fileHash)
		}
		sort.Strings(fileHashes)
		parts = append(parts, fileHashes...)
	}

	for _, subDir := range childDirs[dirPath] {
		if h, ok := dirHash[subDir]; ok {
			parts = append(parts, h)
		}
	}
	sort.Strings(parts)

	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}

// buildFilesSummariesText formats file summaries for the LLM prompt.
func buildFilesSummariesText(files []*fileInfo) string {
	if len(files) == 0 {
		return "(no files)"
	}
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString("- ")
		sb.WriteString(f.filePath)
		sb.WriteString(": ")
		sb.WriteString(f.summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildSubdirsSummariesText formats subdirectory summaries for the LLM prompt.
func buildSubdirsSummariesText(childDirs []string, dirSummary map[string]string) string {
	if len(childDirs) == 0 {
		return "(no subdirectories)"
	}
	var lines []string
	for _, dir := range childDirs {
		if summary, ok := dirSummary[dir]; ok {
			lines = append(lines, "- "+dir+": "+summary)
		}
	}
	if len(lines) == 0 {
		return "(no subdirectories)"
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
