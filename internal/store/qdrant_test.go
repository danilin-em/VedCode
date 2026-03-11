package store

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var noopLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestFilePathToID(t *testing.T) {
	// Deterministic: same input → same output
	id1 := FilePathToID("src/main.go")
	id2 := FilePathToID("src/main.go")
	if id1 != id2 {
		t.Errorf("expected deterministic ID, got %s and %s", id1, id2)
	}

	// Different inputs → different outputs
	id3 := FilePathToID("src/other.go")
	if id1 == id3 {
		t.Errorf("expected different IDs for different paths, got same: %s", id1)
	}

	// UUID v5 format: 8-4-4-4-12 hex chars
	if len(id1) != 36 {
		t.Errorf("expected UUID length 36, got %d: %s", len(id1), id1)
	}

	// Version byte should be 5
	if id1[14] != '5' {
		t.Errorf("expected UUID version 5, got %c in %s", id1[14], id1)
	}
}

func TestEnsureCollection_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":{"status":"green"}}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	if err := s.EnsureCollection(); err != nil {
		t.Fatalf("EnsureCollection failed: %v", err)
	}
}

func TestEnsureCollection_Creates(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			created = true

			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			json.Unmarshal(body, &req)

			vectors := req["vectors"].(map[string]any)
			if vectors["size"].(float64) != 3072 {
				t.Errorf("expected vector size 3072, got %v", vectors["size"])
			}
			if vectors["distance"] != "Cosine" {
				t.Errorf("expected Cosine distance, got %v", vectors["distance"])
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":true}`))
			return
		}
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	if err := s.EnsureCollection(); err != nil {
		t.Fatalf("EnsureCollection failed: %v", err)
	}
	if !created {
		t.Error("expected collection to be created")
	}
}

func TestUpsertPoint(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	err := s.UpsertPoint(&Point{
		Vector:           []float32{0.1, 0.2, 0.3},
		Summary:          "Test summary",
		FilePath:         "src/main.go",
		FileHash:         "abc123",
		Type:             "file",
		Responsibilities: []string{"entry point"},
		Domain:           "Core",
		Language:         "go",
		IndexedAt:        now,
	})
	if err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	points := receivedBody["points"].([]any)
	point := points[0].(map[string]any)

	// Should auto-generate ID from file_path
	if point["id"] == "" {
		t.Error("expected non-empty ID")
	}

	payload := point["payload"].(map[string]any)
	if payload["file_path"] != "src/main.go" {
		t.Errorf("expected file_path src/main.go, got %v", payload["file_path"])
	}
	if payload["summary"] != "Test summary" {
		t.Errorf("expected summary 'Test summary', got %v", payload["summary"])
	}
	if payload["type"] != "file" {
		t.Errorf("expected type file, got %v", payload["type"])
	}
}

func TestUpsertPoint_UsesProvidedID(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	customID := "custom-uuid-12345"
	err := s.UpsertPoint(&Point{
		ID:       customID,
		Vector:   []float32{0.1},
		FilePath: "src/main.go",
		Type:     "file",
	})
	if err != nil {
		t.Fatalf("UpsertPoint failed: %v", err)
	}

	points := receivedBody["points"].([]any)
	point := points[0].(map[string]any)
	if point["id"] != customID {
		t.Errorf("expected ID %s, got %v", customID, point["id"])
	}
}

func TestUpsertPoints(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	err := s.UpsertPoints([]*Point{
		{
			Vector:   []float32{0.1, 0.2},
			Summary:  "First file",
			FilePath: "src/a.go",
			FileHash: "hash1",
			Type:     "file",
			Language: "go",
			IndexedAt: now,
		},
		{
			Vector:   []float32{0.3, 0.4},
			Summary:  "Second file",
			FilePath: "src/b.go",
			FileHash: "hash2",
			Type:     "file",
			Language: "go",
			IndexedAt: now,
		},
	})
	if err != nil {
		t.Fatalf("UpsertPoints failed: %v", err)
	}

	points := receivedBody["points"].([]any)
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}

	p1 := points[0].(map[string]any)
	p1Payload := p1["payload"].(map[string]any)
	if p1Payload["file_path"] != "src/a.go" {
		t.Errorf("expected file_path src/a.go, got %v", p1Payload["file_path"])
	}

	p2 := points[1].(map[string]any)
	p2Payload := p2["payload"].(map[string]any)
	if p2Payload["file_path"] != "src/b.go" {
		t.Errorf("expected file_path src/b.go, got %v", p2Payload["file_path"])
	}
}

func TestUpsertPoints_Empty(t *testing.T) {
	s := NewQdrantStore("http://localhost:6333", "vedcode_", "test", noopLogger)
	err := s.UpsertPoints([]*Point{})
	if err != nil {
		t.Fatalf("UpsertPoints with empty slice should not error: %v", err)
	}
}

func TestGetAllFilePoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// Verify filter
		filter := req["filter"].(map[string]any)
		must := filter["must"].([]any)
		condition := must[0].(map[string]any)
		if condition["key"] != "type" {
			t.Errorf("expected filter key 'type', got %v", condition["key"])
		}

		resp := `{
			"result": {
				"points": [
					{
						"id": "uuid-1",
						"payload": {
							"summary": "Main entry",
							"file_path": "src/main.go",
							"file_hash": "hash1",
							"type": "file",
							"responsibilities": ["entry point"],
							"domain": "Core",
							"language": "go",
							"indexed_at": "2025-01-01T00:00:00Z"
						}
					},
					{
						"id": "uuid-2",
						"payload": {
							"summary": "Config loader",
							"file_path": "src/config.go",
							"file_hash": "hash2",
							"type": "file",
							"responsibilities": ["load config", "validate"],
							"domain": "Infrastructure",
							"language": "go",
							"indexed_at": "2025-01-01T00:00:00Z"
						}
					}
				]
			}
		}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	points, err := s.GetAllFilePoints()
	if err != nil {
		t.Fatalf("GetAllFilePoints failed: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}

	if points[0].FilePath != "src/main.go" {
		t.Errorf("expected file_path src/main.go, got %s", points[0].FilePath)
	}
	if points[0].ID != "uuid-1" {
		t.Errorf("expected ID uuid-1, got %s", points[0].ID)
	}
	if len(points[1].Responsibilities) != 2 {
		t.Errorf("expected 2 responsibilities, got %d", len(points[1].Responsibilities))
	}
}

func TestGetAllDirPoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// Verify filter uses type=directory
		filter := req["filter"].(map[string]any)
		must := filter["must"].([]any)
		condition := must[0].(map[string]any)
		matchVal := condition["match"].(map[string]any)
		if matchVal["value"] != "directory" {
			t.Errorf("expected filter value 'directory', got %v", matchVal["value"])
		}

		resp := `{
			"result": {
				"points": [{
					"id": "uuid-dir-1",
					"payload": {
						"summary": "Storage layer",
						"file_path": "internal/store",
						"file_hash": "dirhash1",
						"type": "directory",
						"responsibilities": ["Vector storage", "REST client"],
						"domain": "Infrastructure",
						"language": "",
						"indexed_at": "2025-01-01T00:00:00Z"
					}
				}]
			}
		}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	points, err := s.GetAllDirPoints()
	if err != nil {
		t.Fatalf("GetAllDirPoints failed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if points[0].Type != "directory" {
		t.Errorf("expected type directory, got %s", points[0].Type)
	}
	if points[0].FilePath != "internal/store" {
		t.Errorf("expected file_path internal/store, got %s", points[0].FilePath)
	}
}

func TestGetPointByFilePath_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{
			"result": {
				"points": [{
					"id": "uuid-1",
					"payload": {
						"summary": "Payment gateway",
						"file_path": "src/payment.go",
						"file_hash": "abc",
						"type": "file",
						"domain": "Payments",
						"language": "go",
						"indexed_at": "2025-01-01T00:00:00Z"
					}
				}]
			}
		}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	point, err := s.GetPointByFilePath("src/payment.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point == nil {
		t.Fatal("expected point, got nil")
	}
	if point.Domain != "Payments" {
		t.Errorf("expected domain Payments, got %s", point.Domain)
	}
}

func TestGetPointByFilePath_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"points":[]}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	point, err := s.GetPointByFilePath("nonexistent.go")
	if err != nil {
		t.Fatalf("GetPointByFilePath failed: %v", err)
	}
	if point != nil {
		t.Errorf("expected nil, got %+v", point)
	}
}

func TestDeletePoints(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	err := s.DeletePoints([]string{"uuid-1", "uuid-2"})
	if err != nil {
		t.Fatalf("DeletePoints failed: %v", err)
	}

	ids := receivedBody["points"].([]any)
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d", len(ids))
	}
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["limit"].(float64) != 3 {
			t.Errorf("expected limit 3, got %v", req["limit"])
		}

		resp := `{
			"result": [
				{
					"id": "uuid-1",
					"score": 0.95,
					"payload": {
						"file_path": "src/payment.go",
						"summary": "Payment processing module"
					}
				},
				{
					"id": "uuid-2",
					"score": 0.82,
					"payload": {
						"file_path": "src/checkout.go",
						"summary": "Checkout flow handler"
					}
				}
			]
		}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	results, err := s.Search([]float32{0.1, 0.2, 0.3}, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].FilePath != "src/payment.go" {
		t.Errorf("expected file_path src/payment.go, got %s", results[0].FilePath)
	}
	if results[0].Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", results[0].Score)
	}
	if results[1].Summary != "Checkout flow handler" {
		t.Errorf("expected summary 'Checkout flow handler', got %s", results[1].Summary)
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["limit"].(float64) != 5 {
			t.Errorf("expected default limit 5, got %v", req["limit"])
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)
	_, err := s.Search([]float32{0.1}, 0)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
}

func TestCollectionName(t *testing.T) {
	s := NewQdrantStore("http://localhost:6333", "vedcode_", "my-app", noopLogger)
	if s.collection != "vedcode_my-app" {
		t.Errorf("expected collection name vedcode_my-app, got %s", s.collection)
	}
}

func TestQdrantError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"status":{"error":"something went wrong"}}`))
	}))
	defer srv.Close()

	s := NewQdrantStore(srv.URL, "vedcode_", "test", noopLogger)

	// EnsureCollection should fail on creation
	err := s.EnsureCollection()
	if err == nil {
		t.Error("expected error, got nil")
	}

	// Search should fail
	_, err = s.Search([]float32{0.1}, 5)
	if err == nil {
		t.Error("expected error, got nil")
	}

	// GetAllFilePoints should fail
	_, err = s.GetAllFilePoints()
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestInterfaceCompliance(t *testing.T) {
	// Compile-time check that QdrantStore implements Store
	var _ Store = (*QdrantStore)(nil)
}
