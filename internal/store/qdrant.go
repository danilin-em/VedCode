package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QdrantStore implements the Store interface using the Qdrant REST API.
type QdrantStore struct {
	baseURL    string
	collection string
	client     *http.Client
}

// NewQdrantStore creates a new Qdrant store client.
func NewQdrantStore(url, collectionPrefix, projectName string) *QdrantStore {
	return &QdrantStore{
		baseURL:    url,
		collection: collectionPrefix + "project_" + projectName,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// EnsureCollection creates the collection if it does not exist.
func (q *QdrantStore) EnsureCollection() error {
	// Check if collection exists
	resp, err := q.client.Get(q.baseURL + "/collections/" + q.collection)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// Create collection with vector size 3072 and cosine distance
	body := map[string]any{
		"vectors": map[string]any{
			"size":     3072,
			"distance": "Cosine",
		},
	}

	return q.put("/collections/"+q.collection, body)
}

// UpsertPoint creates or updates a point in the collection.
func (q *QdrantStore) UpsertPoint(point *Point) error {
	id := point.ID
	if id == "" {
		id = FilePathToID(point.FilePath)
	}

	body := map[string]any{
		"points": []map[string]any{
			{
				"id":     id,
				"vector": point.Vector,
				"payload": map[string]any{
					"summary":          point.Summary,
					"file_path":        point.FilePath,
					"file_hash":        point.FileHash,
					"type":             point.Type,
					"responsibilities": point.Responsibilities,
					"domain":           point.Domain,
					"language":         point.Language,
					"indexed_at":       point.IndexedAt.Format(time.RFC3339),
				},
			},
		},
	}

	return q.put("/collections/"+q.collection+"/points", body)
}

// GetAllFilePoints returns all points with type=file.
func (q *QdrantStore) GetAllFilePoints() ([]*Point, error) {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key": "type",
					"match": map[string]any{
						"value": "file",
					},
				},
			},
		},
		"limit":        1000,
		"with_payload": true,
		"with_vector":  false,
	}

	var result qdrantScrollResponse
	if err := q.postJSON("/collections/"+q.collection+"/points/scroll", body, &result); err != nil {
		return nil, err
	}

	return parsePoints(result.Result.Points), nil
}

// GetPointByFilePath finds a point by its file_path payload field.
func (q *QdrantStore) GetPointByFilePath(path string) (*Point, error) {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key": "file_path",
					"match": map[string]any{
						"value": path,
					},
				},
			},
		},
		"limit":        1,
		"with_payload": true,
		"with_vector":  false,
	}

	var result qdrantScrollResponse
	if err := q.postJSON("/collections/"+q.collection+"/points/scroll", body, &result); err != nil {
		return nil, err
	}

	points := parsePoints(result.Result.Points)
	if len(points) == 0 {
		return nil, nil
	}

	return points[0], nil
}

// DeletePoints deletes points by their IDs.
func (q *QdrantStore) DeletePoints(ids []string) error {
	body := map[string]any{
		"points": ids,
	}

	return q.postExpectOK("/collections/"+q.collection+"/points/delete", body)
}

// Search performs a vector similarity search and returns matching results.
func (q *QdrantStore) Search(vector []float32, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}

	var result qdrantSearchResponse
	if err := q.postJSON("/collections/"+q.collection+"/points/search", body, &result); err != nil {
		return nil, err
	}

	var results []*SearchResult
	for _, p := range result.Result {
		results = append(results, &SearchResult{
			FilePath: getString(p.Payload, "file_path"),
			Summary:  getString(p.Payload, "summary"),
			Score:    p.Score,
		})
	}

	return results, nil
}

// --- HTTP helpers ---

func (q *QdrantStore) put(path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, q.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	return nil
}

func (q *QdrantStore) postJSON(path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := q.client.Post(q.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func (q *QdrantStore) postExpectOK(path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := q.client.Post(q.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	return nil
}

// --- Qdrant response types ---

type qdrantScrollResponse struct {
	Result struct {
		Points []qdrantPoint `json:"points"`
	} `json:"result"`
}

type qdrantSearchResponse struct {
	Result []qdrantSearchResult `json:"result"`
}

type qdrantPoint struct {
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
}

type qdrantSearchResult struct {
	ID      string         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// --- Payload parsing ---

func parsePoints(raw []qdrantPoint) []*Point {
	var points []*Point
	for _, p := range raw {
		point := &Point{
			ID:               p.ID,
			Summary:          getString(p.Payload, "summary"),
			FilePath:         getString(p.Payload, "file_path"),
			FileHash:         getString(p.Payload, "file_hash"),
			Type:             getString(p.Payload, "type"),
			Responsibilities: getStringSlice(p.Payload, "responsibilities"),
			Domain:           getString(p.Payload, "domain"),
			Language:         getString(p.Payload, "language"),
		}
		if ts := getString(p.Payload, "indexed_at"); ts != "" {
			point.IndexedAt, _ = time.Parse(time.RFC3339, ts)
		}
		points = append(points, point)
	}
	return points
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
