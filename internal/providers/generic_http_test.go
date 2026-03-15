package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGenericHTTP_GenerateContent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}

		body, _ := io.ReadAll(r.Body)
		var req chatRequest
		json.Unmarshal(body, &req)

		if req.Model != "gpt-4" {
			t.Errorf("model = %q, want gpt-4", req.Model)
		}
		if req.ResponseFormat != nil {
			t.Error("expected no response_format for GenerateContent")
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Error("expected single user message")
		}

		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "Generated text"}},
			},
		})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "test-key", "gpt-4", "text-embedding-3-small", noopLogger)
	result, err := p.GenerateContent("test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Generated text" {
		t.Errorf("got %q, want %q", result, "Generated text")
	}
}

func TestGenericHTTP_GenerateContent_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(chatResponse{})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	_, err := p.GenerateContent("test")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestGenericHTTP_GenerateContent_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	_, err := p.GenerateContent("test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want to contain 400", err.Error())
	}
}

func TestGenericHTTP_GenerateContent_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when API key is empty")
		}
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}},
			},
		})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "", "llama3.1", "", noopLogger)
	_, err := p.GenerateContent("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenericHTTP_GenerateJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req chatRequest
		json.Unmarshal(body, &req)

		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Error("expected response_format json_object")
		}

		// Schema should be appended to the prompt
		if !strings.Contains(req.Messages[0].Content, `"type":"object"`) {
			t.Error("expected schema in prompt text")
		}

		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: `{"name":"test","value":42}`}},
			},
		})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	schema := `{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"integer"}}}`
	result, err := p.GenerateJSON("analyze this", schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `{"name":"test","value":42}` {
		t.Errorf("got %q", result)
	}
}

func TestGenericHTTP_GenerateJSON_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(chatResponse{})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	_, err := p.GenerateJSON("test", `{"type":"object"}`)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestGenericHTTP_EmbedContent_Success(t *testing.T) {
	expectedVec := []float32{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var req embeddingRequest
		json.Unmarshal(body, &req)

		if req.Model != "text-embedding-3-small" {
			t.Errorf("model = %q, want text-embedding-3-small", req.Model)
		}

		json.NewEncoder(w).Encode(embeddingResponse{
			Data: []embeddingData{
				{Embedding: expectedVec},
			},
		})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "text-embedding-3-small", noopLogger)
	result, err := p.EmbedContent("test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(expectedVec) {
		t.Fatalf("got %d values, want %d", len(result), len(expectedVec))
	}
	for i, v := range result {
		if v != expectedVec[i] {
			t.Errorf("result[%d] = %f, want %f", i, v, expectedVec[i])
		}
	}
}

func TestGenericHTTP_EmbedContent_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(embeddingResponse{})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "", "emb-model", noopLogger)
	_, err := p.EmbedContent("test")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestGenericHTTP_EmbedContent_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "bad-key", "", "emb-model", noopLogger)
	_, err := p.EmbedContent("test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want to contain 401", err.Error())
	}
}

func TestGenericHTTP_RetryOnRateLimit(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
			return
		}
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "success after retries"}},
			},
		})
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	result, err := p.GenerateContent("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "success after retries" {
		t.Errorf("got %q, want %q", result, "success after retries")
	}
	if c := callCount.Load(); c != 3 {
		t.Errorf("call count = %d, want 3", c)
	}
}

func TestGenericHTTP_NoRetryOnClientError(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	p := NewGenericHTTPProvider(srv.URL, "key", "gpt-4", "", noopLogger)
	_, err := p.GenerateContent("test")
	if err == nil {
		t.Fatal("expected error")
	}
	if c := callCount.Load(); c != 1 {
		t.Errorf("call count = %d, want 1 (no retries for 400)", c)
	}
}
