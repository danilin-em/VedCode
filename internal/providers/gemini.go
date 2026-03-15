package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/genai"
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
	logger         *slog.Logger
}

// NewGeminiProvider creates a new GeminiProvider with the given API key and model names.
func NewGeminiProvider(apiKey, model, embeddingModel string, logger *slog.Logger) (*GeminiProvider, error) {
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
		logger:         logger,
	}, nil
}

// newGeminiProviderWithModels creates a GeminiProvider with a custom modelsAPI (for testing).
func newGeminiProviderWithModels(models modelsAPI, model, embeddingModel string, logger *slog.Logger) *GeminiProvider {
	return &GeminiProvider{
		models:         models,
		model:          model,
		embeddingModel: embeddingModel,
		logger:         logger,
	}
}

// GenerateContent sends a prompt to Gemini and returns the generated text.
func (g *GeminiProvider) GenerateContent(prompt string) (string, error) {
	g.logger.Debug("GenerateContent request",
		"model", g.model,
		"prompt", prompt,
	)
	start := time.Now()

	contents := []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}

	var resp *genai.GenerateContentResponse
	err := retryOnRateLimit(g.logger, func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.GenerateContent(ctx, g.model, contents, nil)
		return apiErr
	})
	if err != nil {
		g.logger.Debug("GenerateContent failed",
			"model", g.model,
			"error", err,
			"duration", time.Since(start),
		)
		return "", fmt.Errorf("generating content: %w", err)
	}

	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("generating content: empty response")
	}

	g.logger.Debug("GenerateContent response",
		"model", g.model,
		"response", text,
		"duration", time.Since(start),
	)

	return text, nil
}

// GenerateJSON sends a prompt to Gemini with a JSON response schema and returns the generated JSON string.
func (g *GeminiProvider) GenerateJSON(prompt string, schema string) (string, error) {
	g.logger.Debug("GenerateJSON request",
		"model", g.model,
		"prompt", prompt,
		"schema", schema,
	)
	start := time.Now()

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
	err := retryOnRateLimit(g.logger, func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.GenerateContent(ctx, g.model, contents, config)
		return apiErr
	})
	if err != nil {
		g.logger.Debug("GenerateJSON failed",
			"model", g.model,
			"error", err,
			"duration", time.Since(start),
		)
		return "", fmt.Errorf("generating JSON content: %w", err)
	}

	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("generating JSON content: empty response")
	}

	g.logger.Debug("GenerateJSON response",
		"model", g.model,
		"response", text,
		"duration", time.Since(start),
	)

	return text, nil
}

// EmbedContent sends text to Gemini Embedding API and returns the embedding vector.
func (g *GeminiProvider) EmbedContent(text string) ([]float32, error) {
	g.logger.Debug("EmbedContent request",
		"model", g.embeddingModel,
		"text", text,
	)
	start := time.Now()

	contents := []*genai.Content{
		genai.NewContentFromText(text, genai.RoleUser),
	}

	var resp *genai.EmbedContentResponse
	err := retryOnRateLimit(g.logger, func(ctx context.Context) error {
		var apiErr error
		resp, apiErr = g.models.EmbedContent(ctx, g.embeddingModel, contents, nil)
		return apiErr
	})
	if err != nil {
		g.logger.Debug("EmbedContent failed",
			"model", g.embeddingModel,
			"error", err,
			"duration", time.Since(start),
		)
		return nil, fmt.Errorf("embedding content: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("embedding content: empty response")
	}

	g.logger.Debug("EmbedContent response",
		"model", g.embeddingModel,
		"vector_dim", len(resp.Embeddings[0].Values),
		"duration", time.Since(start),
	)

	return resp.Embeddings[0].Values, nil
}

// DetectVectorSize returns the embedding dimensionality by generating a test embedding.
func (g *GeminiProvider) DetectVectorSize() (int, error) {
	vec, err := g.EmbedContent("test")
	if err != nil {
		return 0, fmt.Errorf("detect vector size: %w", err)
	}
	return len(vec), nil
}
