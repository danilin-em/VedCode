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
	"strings"
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

// pointMeta stores metadata for each point in a side-index.
// chromem-go lacks a "list all documents" API, so we maintain
// this index for GetAllFilePoints/GetAllDirPoints operations.
type pointMeta struct {
	ID               string    `json:"id"`
	FilePath         string    `json:"file_path"`
	FileHash         string    `json:"file_hash"`
	Type             string    `json:"type"`
	Summary          string    `json:"summary"`
	Responsibilities []string  `json:"responsibilities,omitempty"`
	Domain           string    `json:"domain,omitempty"`
	Language         string    `json:"language,omitempty"`
	IndexedAt        time.Time `json:"indexed_at"`
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

// UpsertPoints creates or updates multiple points in a single batch.
func (s *ChromemStore) UpsertPoints(ctx context.Context, points []*Point) error {
	if len(points) == 0 {
		return nil
	}

	col, err := s.requireCollection()
	if err != nil {
		return err
	}

	s.logger.Debug("UpsertPoints", "count", len(points))

	// Delete existing documents
	ids := make([]string, 0, len(points))
	for _, p := range points {
		id := p.ID
		if id == "" {
			id = FilePathToID(p.FilePath)
		}
		ids = append(ids, id)
	}
	if err := col.Delete(ctx, nil, nil, ids...); err != nil {
		s.logger.Warn("delete before upsert failed", "count", len(ids), "error", err)
	}

	// Add all documents
	docs := make([]chromem.Document, 0, len(points))
	for i, p := range points {
		docs = append(docs, pointToDocument(ids[i], p))
	}

	if err := col.AddDocuments(ctx, docs, 1); err != nil {
		return fmt.Errorf("add documents: %w", err)
	}

	s.mu.Lock()
	for i, p := range points {
		s.index[ids[i]] = pointToMeta(ids[i], p)
	}
	s.dirty = true
	s.mu.Unlock()

	return nil
}

// GetAllFilePoints returns all points with type=file from the metadata index.
func (s *ChromemStore) GetAllFilePoints(_ context.Context) ([]*Point, error) {
	s.logger.Debug("GetAllFilePoints")
	start := time.Now()

	s.mu.RLock()
	defer s.mu.RUnlock()

	var points []*Point
	for _, m := range s.index {
		if m.Type == "file" {
			points = append(points, metaToPoint(m))
		}
	}

	s.logger.Debug("GetAllFilePoints completed",
		"count", len(points),
		"duration", time.Since(start),
	)
	return points, nil
}

// GetAllDirPoints returns all points with type=directory from the metadata index.
func (s *ChromemStore) GetAllDirPoints(_ context.Context) ([]*Point, error) {
	s.logger.Debug("GetAllDirPoints")
	start := time.Now()

	s.mu.RLock()
	defer s.mu.RUnlock()

	var points []*Point
	for _, m := range s.index {
		if m.Type == "directory" {
			points = append(points, metaToPoint(m))
		}
	}

	s.logger.Debug("GetAllDirPoints completed",
		"count", len(points),
		"duration", time.Since(start),
	)
	return points, nil
}

// GetPointByFilePath finds a point by its file_path.
func (s *ChromemStore) GetPointByFilePath(_ context.Context, path string) (*Point, error) {
	s.logger.Debug("GetPointByFilePath", "path", path)

	id := FilePathToID(path)

	s.mu.RLock()
	m, ok := s.index[id]
	s.mu.RUnlock()

	if !ok {
		s.logger.Debug("GetPointByFilePath: not found", "path", path)
		return nil, nil
	}

	return metaToPoint(m), nil
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
		metadata["responsibilities"] = strings.Join(p.Responsibilities, "||")
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

func pointToMeta(id string, p *Point) *pointMeta {
	var resps []string
	if len(p.Responsibilities) > 0 {
		resps = make([]string, len(p.Responsibilities))
		copy(resps, p.Responsibilities)
	}
	return &pointMeta{
		ID:               id,
		FilePath:         p.FilePath,
		FileHash:         p.FileHash,
		Type:             p.Type,
		Summary:          p.Summary,
		Responsibilities: resps,
		Domain:           p.Domain,
		Language:         p.Language,
		IndexedAt:        p.IndexedAt,
	}
}

func metaToPoint(m *pointMeta) *Point {
	var resps []string
	if len(m.Responsibilities) > 0 {
		resps = make([]string, len(m.Responsibilities))
		copy(resps, m.Responsibilities)
	}
	return &Point{
		ID:               m.ID,
		FilePath:         m.FilePath,
		FileHash:         m.FileHash,
		Type:             m.Type,
		Summary:          m.Summary,
		Responsibilities: resps,
		Domain:           m.Domain,
		Language:         m.Language,
		IndexedAt:        m.IndexedAt,
	}
}
