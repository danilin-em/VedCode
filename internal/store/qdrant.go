package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// QdrantStore implements the Store interface using the Qdrant REST API.
type QdrantStore struct {
	baseURL    string
	collection string
	vectorSize int
	client     *http.Client
	logger     *slog.Logger
}

// NewQdrantStore creates a new Qdrant store client.
func NewQdrantStore(url, collectionPrefix, projectName string, vectorSize int, logger *slog.Logger) *QdrantStore {
	return &QdrantStore{
		baseURL:    url,
		collection: collectionPrefix + projectName,
		vectorSize: vectorSize,
		client:     &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
}

// EnsureCollection creates the collection if it does not exist.
func (q *QdrantStore) EnsureCollection(ctx context.Context) error {
	q.logger.Debug("EnsureCollection", "collection", q.collection)

	// Check if collection exists
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/collections/"+q.collection, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		q.logger.Debug("EnsureCollection: already exists", "collection", q.collection)
		return nil
	}

	q.logger.Debug("EnsureCollection: creating", "collection", q.collection)

	// Create collection with configured vector size and cosine distance
	body := map[string]any{
		"vectors": map[string]any{
			"size":     q.vectorSize,
			"distance": "Cosine",
		},
	}

	return q.put(ctx, "/collections/"+q.collection, body)
}

// DeleteCollection deletes the entire collection from Qdrant.
func (q *QdrantStore) DeleteCollection(ctx context.Context) error {
	q.logger.Debug("DeleteCollection", "collection", q.collection)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, q.baseURL+"/collections/"+q.collection, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("qdrant delete collection returned %d (body unreadable: %w)", resp.StatusCode, err)
		}
		return fmt.Errorf("qdrant delete collection returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// UpsertPoint creates or updates a point in the collection.
func (q *QdrantStore) UpsertPoint(ctx context.Context, point *Point) error {
	id := point.ID
	if id == "" {
		id = FilePathToID(point.FilePath)
	}

	q.logger.Debug("UpsertPoint",
		"file_path", point.FilePath,
		"point_id", id,
		"type", point.Type,
	)

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

	return q.put(ctx, "/collections/"+q.collection+"/points", body)
}

// UpsertPoints creates or updates multiple points in the collection in a single request.
func (q *QdrantStore) UpsertPoints(ctx context.Context, points []*Point) error {
	if len(points) == 0 {
		return nil
	}

	q.logger.Debug("UpsertPoints", "count", len(points))

	rawPoints := make([]map[string]any, 0, len(points))
	for _, point := range points {
		id := point.ID
		if id == "" {
			id = FilePathToID(point.FilePath)
		}
		rawPoints = append(rawPoints, map[string]any{
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
		})
	}

	body := map[string]any{
		"points": rawPoints,
	}

	return q.put(ctx, "/collections/"+q.collection+"/points", body)
}

// GetAllFilePoints returns all points with type=file, paginating through all results.
func (q *QdrantStore) GetAllFilePoints(ctx context.Context) ([]*Point, error) {
	q.logger.Debug("GetAllFilePoints")
	start := time.Now()

	filter := map[string]any{
		"must": []map[string]any{
			{
				"key": "type",
				"match": map[string]any{
					"value": "file",
				},
			},
		},
	}

	points, err := q.scrollAll(ctx, filter)
	if err != nil {
		return nil, err
	}

	q.logger.Debug("GetAllFilePoints completed",
		"count", len(points),
		"duration", time.Since(start),
	)
	return points, nil
}

// GetAllDirPoints returns all points with type=directory, paginating through all results.
func (q *QdrantStore) GetAllDirPoints(ctx context.Context) ([]*Point, error) {
	q.logger.Debug("GetAllDirPoints")
	start := time.Now()

	filter := map[string]any{
		"must": []map[string]any{
			{
				"key": "type",
				"match": map[string]any{
					"value": "directory",
				},
			},
		},
	}

	points, err := q.scrollAll(ctx, filter)
	if err != nil {
		return nil, err
	}

	q.logger.Debug("GetAllDirPoints completed",
		"count", len(points),
		"duration", time.Since(start),
	)
	return points, nil
}

// scrollAll paginates through all Qdrant scroll results for a given filter.
func (q *QdrantStore) scrollAll(ctx context.Context, filter map[string]any) ([]*Point, error) {
	const pageSize = 1000
	var allPoints []*Point
	var offset any // nil for first page, then next_page_offset

	for {
		body := map[string]any{
			"filter":       filter,
			"limit":        pageSize,
			"with_payload": true,
			"with_vector":  false,
		}
		if offset != nil {
			body["offset"] = offset
		}

		var result qdrantScrollResponse
		if err := q.postJSON(ctx, "/collections/"+q.collection+"/points/scroll", body, &result); err != nil {
			return nil, err
		}

		allPoints = append(allPoints, parsePoints(result.Result.Points)...)

		if result.Result.NextPageOffset == nil || len(result.Result.Points) < pageSize {
			break
		}
		offset = result.Result.NextPageOffset
	}

	return allPoints, nil
}

// GetPointByFilePath finds a point by its file_path payload field.
func (q *QdrantStore) GetPointByFilePath(ctx context.Context, path string) (*Point, error) {
	q.logger.Debug("GetPointByFilePath", "path", path)

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
	if err := q.postJSON(ctx, "/collections/"+q.collection+"/points/scroll", body, &result); err != nil {
		return nil, err
	}

	points := parsePoints(result.Result.Points)
	if len(points) == 0 {
		q.logger.Debug("GetPointByFilePath: not found", "path", path)
		return nil, nil
	}

	return points[0], nil
}

// DeletePoints deletes points by their IDs.
func (q *QdrantStore) DeletePoints(ctx context.Context, ids []string) error {
	q.logger.Debug("DeletePoints", "count", len(ids))

	body := map[string]any{
		"points": ids,
	}

	return q.postExpectOK(ctx, "/collections/"+q.collection+"/points/delete", body)
}

// Flush is a no-op for QdrantStore; Qdrant persists data on every write.
func (q *QdrantStore) Flush(_ context.Context) error { return nil }

// Search performs a vector similarity search and returns matching results.
func (q *QdrantStore) Search(ctx context.Context, vector []float32, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	q.logger.Debug("Search",
		"vector_dim", len(vector),
		"limit", limit,
	)
	start := time.Now()

	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}

	var result qdrantSearchResponse
	if err := q.postJSON(ctx, "/collections/"+q.collection+"/points/search", body, &result); err != nil {
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

	q.logger.Debug("Search completed",
		"results_count", len(results),
		"duration", time.Since(start),
	)

	return results, nil
}

// --- HTTP helpers ---

func (q *QdrantStore) put(ctx context.Context, path string, body any) error {
	start := time.Now()

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	q.logger.Debug("HTTP PUT request",
		"path", path,
		"body", string(data),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, q.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	q.logger.Debug("HTTP PUT response",
		"path", path,
		"status", resp.StatusCode,
		"body", string(respBody),
		"duration", time.Since(start),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	return nil
}

func (q *QdrantStore) postJSON(ctx context.Context, path string, body any, result any) error {
	start := time.Now()

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	q.logger.Debug("HTTP POST request",
		"path", path,
		"body", string(data),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	q.logger.Debug("HTTP POST response",
		"path", path,
		"status", resp.StatusCode,
		"body", string(respBody),
		"duration", time.Since(start),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func (q *QdrantStore) postExpectOK(ctx context.Context, path string, body any) error {
	start := time.Now()

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	q.logger.Debug("HTTP POST request",
		"path", path,
		"body", string(data),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	q.logger.Debug("HTTP POST response",
		"path", path,
		"status", resp.StatusCode,
		"body", string(respBody),
		"duration", time.Since(start),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	return nil
}

// --- Qdrant response types ---

type qdrantScrollResponse struct {
	Result struct {
		Points         []qdrantPoint `json:"points"`
		NextPageOffset any           `json:"next_page_offset"`
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
