package providers

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/genai"
)

// mockModels implements modelsAPI for testing.
type mockModels struct {
	generateResp *genai.GenerateContentResponse
	generateErr  error
	embedResp    *genai.EmbedContentResponse
	embedErr     error

	// captured arguments
	lastModel    string
	lastContents []*genai.Content
}

func (m *mockModels) GenerateContent(_ context.Context, model string, contents []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	m.lastModel = model
	m.lastContents = contents
	return m.generateResp, m.generateErr
}

func (m *mockModels) EmbedContent(_ context.Context, model string, contents []*genai.Content, _ *genai.EmbedContentConfig) (*genai.EmbedContentResponse, error) {
	m.lastModel = model
	m.lastContents = contents
	return m.embedResp, m.embedErr
}

func TestGenerateContent_Success(t *testing.T) {
	text := "Generated response"
	mock := &mockModels{
		generateResp: &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							genai.NewPartFromText(text),
						},
					},
				},
			},
		},
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	result, err := provider.GenerateContent("test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
	if mock.lastModel != "gemini-2.5-flash" {
		t.Errorf("model = %q, want %q", mock.lastModel, "gemini-2.5-flash")
	}
}

func TestGenerateContent_APIError(t *testing.T) {
	mock := &mockModels{
		generateErr: fmt.Errorf("API error: invalid request"),
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	_, err := provider.GenerateContent("test prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "generating content: API error: invalid request" {
		t.Errorf("error = %q, want wrapped API error", got)
	}
}

func TestGenerateContent_EmptyResponse(t *testing.T) {
	mock := &mockModels{
		generateResp: &genai.GenerateContentResponse{},
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	_, err := provider.GenerateContent("test prompt")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
}

func TestEmbedContent_Success(t *testing.T) {
	expectedVec := []float32{0.1, 0.2, 0.3}
	mock := &mockModels{
		embedResp: &genai.EmbedContentResponse{
			Embeddings: []*genai.ContentEmbedding{
				{Values: expectedVec},
			},
		},
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	result, err := provider.EmbedContent("test text")
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
	if mock.lastModel != "gemini-embedding-001" {
		t.Errorf("model = %q, want %q", mock.lastModel, "gemini-embedding-001")
	}
}

func TestEmbedContent_APIError(t *testing.T) {
	mock := &mockModels{
		embedErr: fmt.Errorf("API error: forbidden"),
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	_, err := provider.EmbedContent("test text")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "embedding content: API error: forbidden" {
		t.Errorf("error = %q, want wrapped API error", got)
	}
}

func TestEmbedContent_EmptyResponse(t *testing.T) {
	mock := &mockModels{
		embedResp: &genai.EmbedContentResponse{},
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	_, err := provider.EmbedContent("test text")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
}

func TestRetryOnRateLimit(t *testing.T) {
	callCount := 0
	mock := &mockModels{
		generateErr: fmt.Errorf("429 rate limit exceeded"),
	}

	// Override generateErr to succeed on 3rd attempt
	mock2 := &rateLimitMock{
		failCount: 2,
		successResp: &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							genai.NewPartFromText("success after retries"),
						},
					},
				},
			},
		},
		callCount: &callCount,
	}

	provider := newGeminiProviderWithModels(mock2, "gemini-2.5-flash", "gemini-embedding-001")

	result, err := provider.GenerateContent("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "success after retries" {
		t.Errorf("got %q, want %q", result, "success after retries")
	}
	if callCount != 3 {
		t.Errorf("call count = %d, want 3", callCount)
	}

	_ = mock // prevent unused variable
}

func TestNoRetryOnNonRateLimitError(t *testing.T) {
	callCount := 0
	mock := &rateLimitMock{
		failCount:   10,
		failErr:     fmt.Errorf("invalid API key"),
		callCount:   &callCount,
		successResp: &genai.GenerateContentResponse{},
	}

	provider := newGeminiProviderWithModels(mock, "gemini-2.5-flash", "gemini-embedding-001")

	_, err := provider.GenerateContent("test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 1 {
		t.Errorf("call count = %d, want 1 (no retries for non-rate-limit errors)", callCount)
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("429 Too Many Requests"), true},
		{fmt.Errorf("rate limit exceeded"), true},
		{fmt.Errorf("RESOURCE_EXHAUSTED"), true},
		{fmt.Errorf("quota exceeded"), true},
		{fmt.Errorf("invalid API key"), false},
		{fmt.Errorf("network error"), false},
	}

	for _, tt := range tests {
		got := isRateLimitError(tt.err)
		if got != tt.want {
			t.Errorf("isRateLimitError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// rateLimitMock simulates rate limiting followed by success.
type rateLimitMock struct {
	failCount   int
	failErr     error
	successResp *genai.GenerateContentResponse
	callCount   *int
}

func (m *rateLimitMock) GenerateContent(_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	*m.callCount++
	if *m.callCount <= m.failCount {
		if m.failErr != nil {
			return nil, m.failErr
		}
		return nil, fmt.Errorf("429 rate limit exceeded")
	}
	return m.successResp, nil
}

func (m *rateLimitMock) EmbedContent(_ context.Context, _ string, _ []*genai.Content, _ *genai.EmbedContentConfig) (*genai.EmbedContentResponse, error) {
	return nil, fmt.Errorf("not implemented in rateLimitMock")
}
