package store

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log/slog"
	"time"

	"VedCode/internal/config"
)

// Point represents a stored record in the vector database.
type Point struct {
	ID               string    `json:"id"`
	Vector           []float32 `json:"vector,omitempty"`
	Summary          string    `json:"summary"`
	FilePath         string    `json:"file_path"`
	FileHash         string    `json:"file_hash"`
	Type             string    `json:"type"`
	Responsibilities []string  `json:"responsibilities"`
	Domain           string    `json:"domain"`
	Language         string    `json:"language"`
	IndexedAt        time.Time `json:"indexed_at"`
}

// PathInfo holds lightweight metadata returned by ListPaths.
type PathInfo struct {
	FileHash string
	Summary  string
}

// SearchResult represents a single search result from vector similarity search.
type SearchResult struct {
	FilePath string  `json:"file_path"`
	Summary  string  `json:"summary"`
	Score    float32 `json:"score"`
}

// Store defines the interface for vector storage operations.
type Store interface {
	// Management
	EnsureCollection(ctx context.Context) error
	DeleteCollection(ctx context.Context) error
	Flush(ctx context.Context) error

	// Write
	UpsertPoint(ctx context.Context, point *Point) error
	DeletePoints(ctx context.Context, ids []string) error

	// Read (full Point from underlying storage)
	GetPointByFilePath(ctx context.Context, path string) (*Point, error)

	// Lightweight lists (from side-index / filtered scroll, no vectors)
	// Returns map[filePath]PathInfo for the given point type ("file" or "directory").
	ListPaths(ctx context.Context, pointType string) (map[string]PathInfo, error)

	// Search
	Search(ctx context.Context, vector []float32, limit int) ([]*SearchResult, error)
}

// NewStore creates a Store implementation based on config.
func NewStore(cfg config.StorageConfig, projectName string, vectorSize int, logger *slog.Logger) (Store, error) {
	switch cfg.Type {
	case "qdrant":
		return NewQdrantStore(cfg.URL, cfg.CollectionPrefix, projectName, vectorSize, logger), nil
	case "embedded":
		return NewChromemStore(cfg.Path, cfg.CollectionPrefix, projectName, vectorSize, logger)
	default:
		return nil, fmt.Errorf("unsupported storage.type: %q (supported: qdrant, embedded)", cfg.Type)
	}
}

// FilePathToID generates a deterministic UUID v5 from a file path.
// Uses the DNS namespace as the base UUID (standard UUID v5 convention).
func FilePathToID(filePath string) string {
	// UUID v5 namespace: DNS (6ba7b810-9dad-11d1-80b4-00c04fd430c8)
	namespace := [16]byte{
		0x6b, 0xa7, 0xb8, 0x10,
		0x9d, 0xad, 0x11, 0xd1,
		0x80, 0xb4, 0x00, 0xc0,
		0x4f, 0xd4, 0x30, 0xc8,
	}

	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(filePath))
	sum := h.Sum(nil)

	// Set version to 5
	sum[6] = (sum[6] & 0x0f) | 0x50
	// Set variant to RFC 4122
	sum[8] = (sum[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
