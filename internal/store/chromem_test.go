package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestChromemStore(t *testing.T) *ChromemStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewChromemStore(dir, "test_", "project", 3, noopLogger)
	if err != nil {
		t.Fatalf("NewChromemStore failed: %v", err)
	}
	if err := s.EnsureCollection(context.Background()); err != nil {
		t.Fatalf("EnsureCollection failed: %v", err)
	}
	return s
}

func TestChromemInterfaceCompliance(t *testing.T) {
	var _ Store = (*ChromemStore)(nil)
}

func TestChromemEnsureCollection(t *testing.T) {
	s := newTestChromemStore(t)
	if s.collection.Load() == nil {
		t.Fatal("expected collection to be created")
	}
}

func TestChromemUpsertAndGetByFilePath(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	err := s.UpsertPoint(ctx, &Point{
		Vector:           []float32{0.1, 0.2, 0.3},
		Summary:          "Main entry point",
		FilePath:         "src/main.go",
		FileHash:         "abc123",
		Type:             "file",
		Responsibilities: []string{"entry point", "CLI"},
		Domain:           "Core",
		Language:         "go",
		IndexedAt:        now,
	})
	if err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	point, err := s.GetPointByFilePath(ctx, "src/main.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point == nil {
		t.Fatal("expected point, got nil")
	}
	if point.FilePath != "src/main.go" {
		t.Errorf("expected file_path src/main.go, got %s", point.FilePath)
	}
	if point.Summary != "Main entry point" {
		t.Errorf("expected summary 'Main entry point', got %s", point.Summary)
	}
	if point.FileHash != "abc123" {
		t.Errorf("expected file_hash abc123, got %s", point.FileHash)
	}
	if point.Type != "file" {
		t.Errorf("expected type file, got %s", point.Type)
	}
	if point.Domain != "Core" {
		t.Errorf("expected domain Core, got %s", point.Domain)
	}
	if point.Language != "go" {
		t.Errorf("expected language go, got %s", point.Language)
	}
	if len(point.Responsibilities) != 2 {
		t.Errorf("expected 2 responsibilities, got %d", len(point.Responsibilities))
	}
}

func TestChromemUpsertOverwrite(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	err := s.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.1, 0.2, 0.3},
		Summary:  "Version 1",
		FilePath: "src/main.go",
		FileHash: "hash1",
		Type:     "file",
	})
	if err != nil {
		t.Fatalf("first UpsertPoint failed: %v", err)
	}

	err = s.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.4, 0.5, 0.6},
		Summary:  "Version 2",
		FilePath: "src/main.go",
		FileHash: "hash2",
		Type:     "file",
	})
	if err != nil {
		t.Fatalf("second UpsertPoint failed: %v", err)
	}

	point, err := s.GetPointByFilePath(ctx, "src/main.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point.Summary != "Version 2" {
		t.Errorf("expected updated summary 'Version 2', got %s", point.Summary)
	}
	if point.FileHash != "hash2" {
		t.Errorf("expected updated hash 'hash2', got %s", point.FileHash)
	}
}

func TestChromemUpsertPoints(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	err := s.UpsertPoints(ctx, []*Point{
		{
			Vector:    []float32{0.1, 0.2, 0.3},
			Summary:   "File A",
			FilePath:  "src/a.go",
			FileHash:  "hashA",
			Type:      "file",
			Language:  "go",
			IndexedAt: now,
		},
		{
			Vector:    []float32{0.4, 0.5, 0.6},
			Summary:   "File B",
			FilePath:  "src/b.go",
			FileHash:  "hashB",
			Type:      "file",
			Language:  "go",
			IndexedAt: now,
		},
	})
	if err != nil {
		t.Fatalf("UpsertPoints failed: %v", err)
	}

	points, err := s.GetAllFilePoints(ctx)
	if err != nil {
		t.Fatalf("GetAllFilePoints failed: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
}

func TestChromemUpsertPointsEmpty(t *testing.T) {
	s := newTestChromemStore(t)
	err := s.UpsertPoints(context.Background(), []*Point{})
	if err != nil {
		t.Fatalf("UpsertPoints with empty slice should not error: %v", err)
	}
}

func TestChromemGetAllFilePoints(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	points := []*Point{
		{Vector: []float32{0.1, 0.2, 0.3}, Summary: "File", FilePath: "src/main.go", Type: "file"},
		{Vector: []float32{0.4, 0.5, 0.6}, Summary: "Dir", FilePath: "src", Type: "directory"},
		{Vector: []float32{0.7, 0.8, 0.9}, Summary: "File 2", FilePath: "src/util.go", Type: "file"},
	}
	for _, p := range points {
		if err := s.UpsertPoint(ctx, p); err != nil {
			t.Fatalf("UpsertPoint failed: %v", err)
		}
	}

	filePoints, err := s.GetAllFilePoints(ctx)
	if err != nil {
		t.Fatalf("GetAllFilePoints failed: %v", err)
	}
	if len(filePoints) != 2 {
		t.Fatalf("expected 2 file points, got %d", len(filePoints))
	}

	for _, p := range filePoints {
		if p.Type != "file" {
			t.Errorf("expected type file, got %s", p.Type)
		}
	}
}

func TestChromemGetAllDirPoints(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	points := []*Point{
		{Vector: []float32{0.1, 0.2, 0.3}, Summary: "File", FilePath: "src/main.go", Type: "file"},
		{Vector: []float32{0.4, 0.5, 0.6}, Summary: "Dir 1", FilePath: "src", Type: "directory"},
		{Vector: []float32{0.7, 0.8, 0.9}, Summary: "Dir 2", FilePath: "internal", Type: "directory"},
	}
	for _, p := range points {
		if err := s.UpsertPoint(ctx, p); err != nil {
			t.Fatalf("UpsertPoint failed: %v", err)
		}
	}

	dirPoints, err := s.GetAllDirPoints(ctx)
	if err != nil {
		t.Fatalf("GetAllDirPoints failed: %v", err)
	}
	if len(dirPoints) != 2 {
		t.Fatalf("expected 2 dir points, got %d", len(dirPoints))
	}

	for _, p := range dirPoints {
		if p.Type != "directory" {
			t.Errorf("expected type directory, got %s", p.Type)
		}
	}
}

func TestChromemGetPointByFilePath_NotFound(t *testing.T) {
	s := newTestChromemStore(t)

	point, err := s.GetPointByFilePath(context.Background(), "nonexistent.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point != nil {
		t.Errorf("expected nil, got %+v", point)
	}
}

func TestChromemDeletePoints(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	if err := s.UpsertPoint(ctx, &Point{
		Vector: []float32{0.1, 0.2, 0.3}, Summary: "To delete", FilePath: "src/old.go", Type: "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	id := FilePathToID("src/old.go")
	if err := s.DeletePoints(ctx, []string{id}); err != nil {
		t.Fatalf("DeletePoints failed: %v", err)
	}

	point, err := s.GetPointByFilePath(ctx, "src/old.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point != nil {
		t.Errorf("expected nil after delete, got %+v", point)
	}
}

func TestChromemSearch(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	if err := s.UpsertPoint(ctx, &Point{
		Vector:   []float32{1.0, 0.0, 0.0},
		Summary:  "Payment processing",
		FilePath: "src/payment.go",
		Type:     "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	if err := s.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.0, 1.0, 0.0},
		Summary:  "User authentication",
		FilePath: "src/auth.go",
		Type:     "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	results, err := s.Search(ctx, []float32{0.9, 0.1, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result should be payment.go (closer to query vector)
	if results[0].FilePath != "src/payment.go" {
		t.Errorf("expected first result src/payment.go, got %s", results[0].FilePath)
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", results[0].Score)
	}
	if results[0].Summary != "Payment processing" {
		t.Errorf("expected summary 'Payment processing', got %s", results[0].Summary)
	}
}

func TestChromemSearch_DefaultLimit(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	if err := s.UpsertPoint(ctx, &Point{
		Vector: []float32{1.0, 0.0, 0.0}, Summary: "File", FilePath: "src/a.go", Type: "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	results, err := s.Search(ctx, []float32{1.0, 0.0, 0.0}, 0)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestChromemDeleteCollection(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	if err := s.UpsertPoint(ctx, &Point{
		Vector: []float32{0.1, 0.2, 0.3}, Summary: "File", FilePath: "src/main.go", Type: "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	if err := s.DeleteCollection(ctx); err != nil {
		t.Fatalf("DeleteCollection failed: %v", err)
	}

	// Index should be empty
	points, err := s.GetAllFilePoints(ctx)
	if err != nil {
		t.Fatalf("GetAllFilePoints failed: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 points after delete, got %d", len(points))
	}
}

func TestChromemPersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Create store and add data
	s1, err := NewChromemStore(dir, "test_", "persist", 3, noopLogger)
	if err != nil {
		t.Fatalf("NewChromemStore failed: %v", err)
	}
	if err := s1.EnsureCollection(ctx); err != nil {
		t.Fatalf("EnsureCollection failed: %v", err)
	}

	if err := s1.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.1, 0.2, 0.3},
		Summary:  "Persisted file",
		FilePath: "src/main.go",
		FileHash: "hash123",
		Type:     "file",
		Domain:   "Core",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	if err := s1.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify index file was created
	indexPath := filepath.Join(dir, "test_persist.index.json")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Fatal("expected index file to exist")
	}

	// Open new store with same path — metadata should load from index
	s2, err := NewChromemStore(dir, "test_", "persist", 3, noopLogger)
	if err != nil {
		t.Fatalf("NewChromemStore (reopen) failed: %v", err)
	}
	if err := s2.EnsureCollection(ctx); err != nil {
		t.Fatalf("EnsureCollection (reopen) failed: %v", err)
	}

	point, err := s2.GetPointByFilePath(ctx, "src/main.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point == nil {
		t.Fatal("expected point after reopen, got nil")
	}
	if point.Summary != "Persisted file" {
		t.Errorf("expected summary 'Persisted file', got %s", point.Summary)
	}
	if point.Domain != "Core" {
		t.Errorf("expected domain 'Core', got %s", point.Domain)
	}
}

func TestChromemCollectionName(t *testing.T) {
	dir := t.TempDir()
	s, err := NewChromemStore(dir, "vedcode_", "my-app", 3, noopLogger)
	if err != nil {
		t.Fatalf("NewChromemStore failed: %v", err)
	}
	if s.collName != "vedcode_my-app" {
		t.Errorf("expected collection name vedcode_my-app, got %s", s.collName)
	}
}

func TestChromemUsesProvidedID(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	customID := "custom-uuid-12345"
	if err := s.UpsertPoint(ctx, &Point{
		ID:       customID,
		Vector:   []float32{0.1, 0.2, 0.3},
		FilePath: "src/main.go",
		Type:     "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	s.mu.RLock()
	_, exists := s.index[customID]
	s.mu.RUnlock()

	if !exists {
		t.Error("expected point with custom ID in index")
	}
}

func TestChromemFlush(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := NewChromemStore(dir, "test_", "flush", 3, noopLogger)
	if err != nil {
		t.Fatalf("NewChromemStore failed: %v", err)
	}
	if err := s.EnsureCollection(ctx); err != nil {
		t.Fatalf("EnsureCollection failed: %v", err)
	}

	if err := s.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.1, 0.2, 0.3},
		Summary:  "Flush test",
		FilePath: "src/flush.go",
		FileHash: "hash1",
		Type:     "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	// Data should be available in-memory before flush
	point, err := s.GetPointByFilePath(ctx, "src/flush.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point == nil {
		t.Fatal("expected point in-memory before flush, got nil")
	}

	// Index file should NOT exist yet (no flush)
	indexPath := filepath.Join(dir, "test_flush.index.json")
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatal("expected index file to NOT exist before flush")
	}

	// Flush should write to disk
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Fatal("expected index file to exist after flush")
	}
}

func TestChromemFlushIdempotent(t *testing.T) {
	s := newTestChromemStore(t)
	ctx := context.Background()

	// Flush on clean store should be a no-op
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush on clean store failed: %v", err)
	}

	if err := s.UpsertPoint(ctx, &Point{
		Vector:   []float32{0.1, 0.2, 0.3},
		Summary:  "Test",
		FilePath: "src/a.go",
		Type:     "file",
	}); err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	if err := s.Flush(ctx); err != nil {
		t.Fatalf("first Flush failed: %v", err)
	}

	// Second flush without changes should be a no-op
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("second Flush failed: %v", err)
	}
}
