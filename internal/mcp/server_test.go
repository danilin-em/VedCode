package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"VedCode/internal/store"
)

// mockStore implements store.Store for testing.
type mockStore struct {
	searchResults []*store.SearchResult
	searchErr     error
	point         *store.Point
	pointErr      error
}

func (m *mockStore) EnsureCollection() error                    { return nil }
func (m *mockStore) DeleteCollection() error                    { return nil }
func (m *mockStore) UpsertPoint(point *store.Point) error       { return nil }
func (m *mockStore) UpsertPoints(points []*store.Point) error   { return nil }
func (m *mockStore) GetAllFilePoints() ([]*store.Point, error)  { return nil, nil }
func (m *mockStore) DeletePoints(ids []string) error            { return nil }

func (m *mockStore) Search(vector []float32, limit int) ([]*store.SearchResult, error) {
	return m.searchResults, m.searchErr
}

func (m *mockStore) GetPointByFilePath(path string) (*store.Point, error) {
	return m.point, m.pointErr
}

// mockProvider implements EmbeddingProvider for testing.
type mockProvider struct {
	vector []float32
	err    error
}

func (m *mockProvider) EmbedContent(text string) ([]float32, error) {
	return m.vector, m.err
}

func TestSearchCode_Success(t *testing.T) {
	results := []*store.SearchResult{
		{FilePath: "src/payment.go", Summary: "Payment processing", Score: 0.95},
		{FilePath: "src/order.go", Summary: "Order management", Score: 0.80},
	}

	s := NewServer(
		&mockStore{searchResults: results},
		&mockProvider{vector: []float32{0.1, 0.2, 0.3}},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Name = "search_code"
	req.Params.Arguments = map[string]any{
		"query": "payment processing",
		"limit": float64(2),
	}

	result, err := s.handleSearchCode(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var got []store.SearchResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].FilePath != "src/payment.go" {
		t.Errorf("expected file_path 'src/payment.go', got '%s'", got[0].FilePath)
	}
}

func TestSearchCode_MissingQuery(t *testing.T) {
	s := NewServer(
		&mockStore{},
		&mockProvider{vector: []float32{0.1}},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleSearchCode(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing query")
	}
}

func TestSearchCode_EmbedError(t *testing.T) {
	s := NewServer(
		&mockStore{},
		&mockProvider{err: fmt.Errorf("API error")},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test"}

	result, err := s.handleSearchCode(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for embed failure")
	}
}

func TestSearchCode_StoreError(t *testing.T) {
	s := NewServer(
		&mockStore{searchErr: fmt.Errorf("qdrant down")},
		&mockProvider{vector: []float32{0.1}},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test"}

	result, err := s.handleSearchCode(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for store failure")
	}
}

func TestSearchCode_DefaultLimit(t *testing.T) {
	s := NewServer(
		&mockStore{searchResults: []*store.SearchResult{}},
		&mockProvider{vector: []float32{0.1}},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test"}

	result, err := s.handleSearchCode(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}

func TestGetProjectOverview_Success(t *testing.T) {
	tmpDir := t.TempDir()
	vedcodeDir := filepath.Join(tmpDir, ".vedcode")
	os.MkdirAll(vedcodeDir, 0o755)
	os.WriteFile(filepath.Join(vedcodeDir, "project_overview.md"), []byte("# Project Overview\nGo application"), 0o644)

	s := NewServer(&mockStore{}, &mockProvider{}, tmpDir)

	req := mcp.CallToolRequest{}
	result, err := s.handleGetProjectOverview(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var got map[string]string
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["overview"] != "# Project Overview\nGo application" {
		t.Errorf("unexpected overview content: %s", got["overview"])
	}
}

func TestGetProjectOverview_NotIndexed(t *testing.T) {
	tmpDir := t.TempDir()

	s := NewServer(&mockStore{}, &mockProvider{}, tmpDir)

	req := mcp.CallToolRequest{}
	result, err := s.handleGetProjectOverview(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing overview")
	}
}

func TestGetSummary_Success(t *testing.T) {
	point := &store.Point{
		FilePath:         "src/payment.go",
		Summary:          "Payment gateway",
		Responsibilities: []string{"Process payments", "Handle webhooks"},
		Domain:           "Payments",
	}

	s := NewServer(
		&mockStore{point: point},
		&mockProvider{},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": "src/payment.go"}

	result, err := s.handleGetSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["file_path"] != "src/payment.go" {
		t.Errorf("expected file_path 'src/payment.go', got '%s'", got["file_path"])
	}
	if got["summary"] != "Payment gateway" {
		t.Errorf("expected summary 'Payment gateway', got '%s'", got["summary"])
	}
	if got["domain"] != "Payments" {
		t.Errorf("expected domain 'Payments', got '%s'", got["domain"])
	}
	responsibilities := got["responsibilities"].([]any)
	if len(responsibilities) != 2 {
		t.Errorf("expected 2 responsibilities, got %d", len(responsibilities))
	}
}

func TestGetSummary_MissingFilePath(t *testing.T) {
	s := NewServer(&mockStore{}, &mockProvider{}, "/tmp/test")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleGetSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing file_path")
	}
}

func TestGetSummary_NotIndexed(t *testing.T) {
	s := NewServer(
		&mockStore{point: nil},
		&mockProvider{},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": "nonexistent.go"}

	result, err := s.handleGetSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for non-indexed file")
	}
}

func TestGetSummary_StoreError(t *testing.T) {
	s := NewServer(
		&mockStore{pointErr: fmt.Errorf("connection refused")},
		&mockProvider{},
		"/tmp/test",
	)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": "src/main.go"}

	result, err := s.handleGetSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for store failure")
	}
}
