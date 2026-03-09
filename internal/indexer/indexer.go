package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// Run executes the full indexing cycle for the project.
func Run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rootPath, err := filepath.Abs(cfg.Project.RootPath)
	if err != nil {
		return fmt.Errorf("resolving root path: %w", err)
	}

	// Initialize provider
	provider, err := providers.NewGeminiProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.EmbeddingModel)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	// Initialize store
	db := store.NewQdrantStore(cfg.Storage.URL, cfg.Storage.CollectionPrefix, cfg.Project.Name)
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
	log.Printf("Deleted %d stale records from Qdrant", deletedCount)

	// Analyze project structure via LLM
	structurePrompt, err := prompts.Render("ProjectStructureAnalysis.md", map[string]string{
		"CONTENT": walkResult.Tree,
	})
	if err != nil {
		return fmt.Errorf("rendering project structure prompt: %w", err)
	}

	log.Println("Analyzing project structure...")
	projectOverview, err := provider.GenerateContent(structurePrompt)
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

	skippedCount := 0
	indexedCount := 0
	errorCount := 0

	for i, relPath := range walkResult.Files {
		absPath := filepath.Join(rootPath, relPath)

		// Compute file hash
		content, err := os.ReadFile(absPath)
		if err != nil {
			log.Printf("[%d/%d] Error reading %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
		}

		hash := sha256sum(content)

		// Check if file needs re-indexing
		if existing, ok := existingByPath[relPath]; ok && existing.FileHash == hash {
			skippedCount++
			continue
		}

		log.Printf("[%d/%d] Indexing %s", i+1, len(walkResult.Files), relPath)

		// Analyze file via LLM
		filePrompt, err := prompts.Render("SourceCodeAnalysis.md", map[string]string{
			"CONTENT":          string(content),
			"PROJECT_OVERVIEW": projectOverview,
		})
		if err != nil {
			log.Printf("[%d/%d] Error rendering prompt for %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
		}

		response, err := provider.GenerateContent(filePrompt)
		if err != nil {
			log.Printf("[%d/%d] Error analyzing %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
		}

		analysis, err := parseAnalysis(response)
		if err != nil {
			log.Printf("[%d/%d] Error parsing analysis for %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
		}

		// Get embedding for the summary
		embedding, err := provider.EmbedContent(analysis.Summary)
		if err != nil {
			log.Printf("[%d/%d] Error embedding %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
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
			log.Printf("[%d/%d] Error saving %s: %v", i+1, len(walkResult.Files), relPath, err)
			errorCount++
			continue
		}

		indexedCount++
	}

	// --- Summary ---
	log.Println("\n=== Indexing complete ===")
	log.Printf("Total files:   %d", len(walkResult.Files))
	log.Printf("Indexed:       %d", indexedCount)
	log.Printf("Skipped:       %d (unchanged)", skippedCount)
	log.Printf("Deleted:       %d (removed from project)", deletedCount)
	log.Printf("Errors:        %d", errorCount)

	return nil
}

// sha256sum computes the SHA256 hex digest of data.
func sha256sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// parseAnalysis extracts the JSON block from the LLM response.
func parseAnalysis(response string) (*fileAnalysis, error) {
	// Try to extract JSON from markdown code block
	jsonStr := response
	if start := strings.Index(response, "```json"); start != -1 {
		jsonStr = response[start+7:]
		if end := strings.Index(jsonStr, "```"); end != -1 {
			jsonStr = jsonStr[:end]
		}
	} else if start := strings.Index(response, "```"); start != -1 {
		jsonStr = response[start+3:]
		if end := strings.Index(jsonStr, "```"); end != -1 {
			jsonStr = jsonStr[:end]
		}
	}

	jsonStr = strings.TrimSpace(jsonStr)

	var analysis fileAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if analysis.Summary == "" {
		return nil, fmt.Errorf("empty summary in analysis response")
	}

	return &analysis, nil
}
