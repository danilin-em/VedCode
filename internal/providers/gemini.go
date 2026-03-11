package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	defaultTimeout = 120 * time.Second
	maxRetries     = 3
	baseRetryDelay = time.Second
)

// modelsAPI abstracts the genai Models API for testability.
type modelsAPI interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	EmbedContent(ctx context.Context, model string, contents []*genai.Content, config *genai.EmbedContentConfig) (*genai.EmbedContentResponse, error)
}

// GeminiProvider implements LLM text generation and embedding using Gemini API.
type GeminiProvider struct {
	models         modelsAPI
	model          string
	embeddingModel string
}

// NewGeminiProvider creates a new GeminiProvider with the given API key and model names.
func NewGeminiProvider(apiKey, model, embeddingModel string) (*GeminiProvider, error) {
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating Gemini client: %w", err)
	}

	return &GeminiProvider{
		models:         client.Models,
		model:          model,
		embeddingModel: embeddingModel,
	}, nil
}

// newGeminiProviderWithModels creates a GeminiProvider with a custom modelsAPI (for testing).
func newGeminiProviderWithModels(models modelsAPI, model, embeddingModel string) *GeminiProvider {
	return &GeminiProvider{
		models:         models,
		model:          model,
		embeddingModel: embeddingModel,
	}
}

// GenerateContent sends a prompt to Gemini and returns the generated text.
func (g *GeminiProvider) GenerateContent(prompt string) (string, error) {
	contents := []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}

	var resp *genai.GenerateContentResponse
	err := g.retryOnRateLimit(func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.GenerateContent(ctx, g.model, contents, nil)
		return apiErr
	})
	if err != nil {
		return "", fmt.Errorf("generating content: %w", err)
	}

	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("generating content: empty response")
	}

	return text, nil
}

// GenerateJSON sends a prompt to Gemini with a JSON response schema and returns the generated JSON string.
func (g *GeminiProvider) GenerateJSON(prompt string, schema string) (string, error) {
	var responseSchema genai.Schema
	if err := json.Unmarshal([]byte(schema), &responseSchema); err != nil {
		return "", fmt.Errorf("parsing response schema: %w", err)
	}

	contents := []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}

	config := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr[int32](0),
		},
		ResponseMIMEType: "application/json",
		ResponseSchema:   &responseSchema,
	}

	var resp *genai.GenerateContentResponse
	err := g.retryOnRateLimit(func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.GenerateContent(ctx, g.model, contents, config)
		return apiErr
	})
	if err != nil {
		return "", fmt.Errorf("generating JSON content: %w", err)
	}

	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("generating JSON content: empty response")
	}

	return text, nil
}

// EmbedContent sends text to Gemini Embedding API and returns the embedding vector.
func (g *GeminiProvider) EmbedContent(text string) ([]float32, error) {
	contents := []*genai.Content{
		genai.NewContentFromText(text, genai.RoleUser),
	}

	var resp *genai.EmbedContentResponse
	err := g.retryOnRateLimit(func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.EmbedContent(ctx, g.embeddingModel, contents, nil)
		return apiErr
	})
	if err != nil {
		return nil, fmt.Errorf("embedding content: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("embedding content: empty response")
	}

	return resp.Embeddings[0].Values, nil
}

// retryOnRateLimit retries the given function with exponential backoff on rate limit errors.
func (g *GeminiProvider) retryOnRateLimit(fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		lastErr = fn(ctx)
		cancel()

		if lastErr == nil {
			return nil
		}

		if !isRetryableError(lastErr) {
			return lastErr
		}

		if attempt < maxRetries-1 {
			delay := baseRetryDelay * time.Duration(1<<attempt)
			time.Sleep(delay)
		}
	}
	return lastErr
}

// isRetryableError checks if the error is a rate limit, resource exhaustion, or temporary unavailability error.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "resource exhausted") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "unavailable")
}
