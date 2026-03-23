package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/philippgille/chromem-go"
)

// ChromemStore implements the Store interface using chromem-go embedded vector database.
type ChromemStore struct {
	db         *chromem.DB
	collection atomic.Pointer[chromem.Collection]
	collName   string
	persistDir string
	logger     *slog.Logger

	mu        sync.RWMutex
	index     map[string]*pointMeta
	indexPath string
	dirty     bool
}

var errCollectionNotReady = errors.New("collection not initialized; call EnsureCollection first")

func (s *ChromemStore) requireCollection() (*chromem.Collection, error) {
	col := s.collection.Load()
	if col == nil {
		return nil, errCollectionNotReady
	}
	return col, nil
}

// pointMeta is a thin side-index entry. chromem-go lacks a "list all documents"
// API, so we maintain this minimal index for ListPaths, ID lookups, and
// summary access for unchanged files during incremental indexing.
// Rich metadata (responsibilities, domain, language) lives only in chromem
// document metadata and is retrieved via GetByID when needed.
type pointMeta struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	FileHash string `json:"file_hash"`
	Type     string `json:"type"`
	Summary  string `json:"summary"`
}

// NewChromemStore creates a new embedded vector store backed by chromem-go.
func NewChromemStore(basePath, collectionPrefix, projectName string, vectorSize int, logger *slog.Logger) (*ChromemStore, error) {
	collName := collectionPrefix + projectName
	persistDir := filepath.Join(basePath, collName)

	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating storage directory: %w", err)
	}

	db, err := chromem.NewPersistentDB(persistDir, false)
	if err != nil {
		return nil, fmt.Errorf("opening chromem database: %w", err)
	}

	indexPath := filepath.Join(basePath, collName+".index.json")

	s := &ChromemStore{
		db:         db,
		collName:   collName,
		persistDir: persistDir,
		logger:     logger,
		index:      make(map[string]*pointMeta),
		indexPath:  indexPath,
	}

	if err := s.loadIndex(); err != nil {
		logger.Warn("could not load metadata index, starting fresh", "error", err)
		s.index = make(map[string]*pointMeta)
	}

	return s, nil
}

// EnsureCollection creates the collection if it does not exist.
func (s *ChromemStore) EnsureCollection(_ context.Context) error {
	s.logger.Debug("EnsureCollection", "collection", s.collName)

	// nil embeddingFunc — we always provide pre-computed embeddings
	col, err := s.db.GetOrCreateCollection(s.collName, nil, nil)
	if err != nil {
		return fmt.Errorf("get or create collection: %w", err)
	}
	s.collection.Store(col)

	s.logger.Debug("EnsureCollection: ready", "collection", s.collName, "count", col.Count())
	return nil
}

// DeleteCollection deletes the entire collection and its metadata index.
func (s *ChromemStore) DeleteCollection(_ context.Context) error {
	s.logger.Debug("DeleteCollection", "collection", s.collName)

	if err := s.db.DeleteCollection(s.collName); err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	s.collection.Store(nil)

	s.mu.Lock()
	s.index = make(map[string]*pointMeta)
	s.mu.Unlock()

	if err := os.Remove(s.indexPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing index file: %w", err)
	}

	if err := os.RemoveAll(s.persistDir); err != nil {
		s.logger.Warn("could not remove persistence directory", "error", err)
	}

	return nil
}

// UpsertPoint creates or updates a single point in the collection.
func (s *ChromemStore) UpsertPoint(ctx context.Context, point *Point) error {
	col, err := s.requireCollection()
	if err != nil {
		return err
	}

	id := point.ID
	if id == "" {
		id = FilePathToID(point.FilePath)
	}

	s.logger.Debug("UpsertPoint",
		"file_path", point.FilePath,
		"point_id", id,
		"type", point.Type,
	)

	// Delete existing document if present (chromem-go has no native upsert)
	if err := col.Delete(ctx, nil, nil, id); err != nil {
		s.logger.Warn("delete before upsert failed", "id", id, "error", err)
	}

	doc := pointToDocument(id, point)
	if err := col.AddDocument(ctx, doc); err != nil {
		return fmt.Errorf("add document: %w", err)
	}

	s.mu.Lock()
	s.index[id] = pointToMeta(id, point)
	s.dirty = true
	s.mu.Unlock()

	return nil
}

// ListPaths returns map[filePath]PathInfo for all points of the given type.
// This is a lightweight operation that reads only from the side-index.
func (s *ChromemStore) ListPaths(_ context.Context, pointType string) (map[string]PathInfo, error) {
	s.logger.Debug("ListPaths", "type", pointType)
	start := time.Now()

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]PathInfo)
	for _, m := range s.index {
		if m.Type == pointType {
			result[m.FilePath] = PathInfo{FileHash: m.FileHash, Summary: m.Summary}
		}
	}

	s.logger.Debug("ListPaths completed",
		"type", pointType,
		"count", len(result),
		"duration", time.Since(start),
	)
	return result, nil
}

// GetPointByFilePath finds a point by its file_path.
// Uses the side-index for ID lookup, then chromem GetByID for full metadata.
// Handles both file points (ID from path) and directory points (ID from "dir:"+path).
func (s *ChromemStore) GetPointByFilePath(ctx context.Context, path string) (*Point, error) {
	s.logger.Debug("GetPointByFilePath", "path", path)

	// Try file ID first, then directory ID
	fileID := FilePathToID(path)
	dirID := FilePathToID("dir:" + path)

	s.mu.RLock()
	_, fileOK := s.index[fileID]
	_, dirOK := s.index[dirID]
	s.mu.RUnlock()

	var id string
	switch {
	case fileOK:
		id = fileID
	case dirOK:
		id = dirID
	default:
		s.logger.Debug("GetPointByFilePath: not found", "path", path)
		return nil, nil
	}

	col, err := s.requireCollection()
	if err != nil {
		return nil, err
	}

	doc, err := col.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get document by ID: %w", err)
	}

	return documentToPoint(doc), nil
}

// DeletePoints deletes points by their IDs.
func (s *ChromemStore) DeletePoints(ctx context.Context, ids []string) error {
	col, err := s.requireCollection()
	if err != nil {
		return err
	}

	s.logger.Debug("DeletePoints", "count", len(ids))

	if err := col.Delete(ctx, nil, nil, ids...); err != nil {
		return fmt.Errorf("delete points: %w", err)
	}

	s.mu.Lock()
	for _, id := range ids {
		delete(s.index, id)
	}
	s.dirty = true
	s.mu.Unlock()

	return nil
}

// Search performs a vector similarity search.
func (s *ChromemStore) Search(ctx context.Context, vector []float32, limit int) ([]*SearchResult, error) {
	col, err := s.requireCollection()
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 5
	}

	// chromem-go requires nResults <= collection size
	count := col.Count()
	if count == 0 {
		return nil, nil
	}
	if limit > count {
		limit = count
	}

	s.logger.Debug("Search",
		"vector_dim", len(vector),
		"limit", limit,
	)
	start := time.Now()

	results, err := col.QueryEmbedding(ctx, vector, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}

	var searchResults []*SearchResult
	for _, r := range results {
		searchResults = append(searchResults, &SearchResult{
			FilePath: r.Metadata["file_path"],
			Summary:  r.Content,
			Score:    r.Similarity,
		})
	}

	s.logger.Debug("Search completed",
		"results_count", len(searchResults),
		"duration", time.Since(start),
	)

	return searchResults, nil
}

// --- metadata index persistence ---

func (s *ChromemStore) loadIndex() error {
	data, err := os.ReadFile(s.indexPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading index file: %w", err)
	}

	var entries []*pointMeta
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parsing index file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.index = make(map[string]*pointMeta, len(entries))
	for _, e := range entries {
		s.index[e.ID] = e
	}

	return nil
}

// Flush persists the in-memory metadata index to disk if it has been modified.
// For ChromemStore this writes the JSON index file; callers should invoke Flush
// after completing a batch of mutations (e.g. after indexing finishes).
func (s *ChromemStore) Flush(_ context.Context) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	entries := s.snapshotIndex()
	s.dirty = false
	s.mu.Unlock()

	return s.saveIndex(entries)
}

// snapshotIndex returns a copy of index entries. Must be called with mu held.
func (s *ChromemStore) snapshotIndex() []*pointMeta {
	entries := make([]*pointMeta, 0, len(s.index))
	for _, m := range s.index {
		entries = append(entries, m)
	}
	return entries
}

// saveIndex persists the metadata index to disk.
// Accepts a pre-built entries slice to avoid re-reading the map (TOCTOU safety).
// Note: called on every upsert/delete for durability. During bulk indexing this
// results in O(N²) total write volume — acceptable for correctness, but consider
// batched flushes if performance becomes an issue.
func (s *ChromemStore) saveIndex(entries []*pointMeta) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshalling index: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.indexPath), 0o755); err != nil {
		return fmt.Errorf("creating index directory: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmpPath := s.indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing index file: %w", err)
	}
	if err := os.Rename(tmpPath, s.indexPath); err != nil {
		return fmt.Errorf("renaming index file: %w", err)
	}

	return nil
}

// --- conversion helpers ---

func pointToDocument(id string, p *Point) chromem.Document {
	metadata := map[string]string{
		"file_path": p.FilePath,
		"file_hash": p.FileHash,
		"type":      p.Type,
		"domain":    p.Domain,
		"language":  p.Language,
	}

	if len(p.Responsibilities) > 0 {
		data, _ := json.Marshal(p.Responsibilities)
		metadata["responsibilities"] = string(data)
	}

	if !p.IndexedAt.IsZero() {
		metadata["indexed_at"] = p.IndexedAt.Format(time.RFC3339)
	}

	return chromem.Document{
		ID:        id,
		Content:   p.Summary,
		Metadata:  metadata,
		Embedding: p.Vector,
	}
}

func documentToPoint(doc chromem.Document) *Point {
	var resps []string
	if raw, ok := doc.Metadata["responsibilities"]; ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &resps)
	}
	var indexedAt time.Time
	if raw, ok := doc.Metadata["indexed_at"]; ok {
		indexedAt, _ = time.Parse(time.RFC3339, raw)
	}
	return &Point{
		ID:               doc.ID,
		Summary:          doc.Content,
		FilePath:         doc.Metadata["file_path"],
		FileHash:         doc.Metadata["file_hash"],
		Type:             doc.Metadata["type"],
		Responsibilities: resps,
		Domain:           doc.Metadata["domain"],
		Language:         doc.Metadata["language"],
		IndexedAt:        indexedAt,
	}
}

func pointToMeta(id string, p *Point) *pointMeta {
	return &pointMeta{
		ID:       id,
		FilePath: p.FilePath,
		FileHash: p.FileHash,
		Type:     p.Type,
		Summary:  p.Summary,
	}
}
