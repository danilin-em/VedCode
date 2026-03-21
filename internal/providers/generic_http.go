package providers

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

// OpenAI-compatible API request/response types

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
}

// httpDoer abstracts HTTP client for testability.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// GenericHTTPProvider implements TextGenerator and EmbeddingProvider
// using the OpenAI-compatible chat completions and embeddings API.
// It works with any server that implements the OpenAI HTTP protocol
// (Ollama, LM Studio, vLLM, Google Gemini via OpenAI endpoint, etc.)
type GenericHTTPProvider struct {
	client         httpDoer
	baseURL        string
	apiKey         string
	model          string
	embeddingModel string
	logger         *slog.Logger
}

// NewGenericHTTPProvider creates a new OpenAI-compatible HTTP provider.
func NewGenericHTTPProvider(baseURL, apiKey, model, embeddingModel string, logger *slog.Logger) *GenericHTTPProvider {
	return &GenericHTTPProvider{
		client:         &http.Client{Timeout: defaultTimeout},
		baseURL:        baseURL,
		apiKey:         apiKey,
		model:          model,
		embeddingModel: embeddingModel,
		logger:         logger,
	}
}

// newGenericHTTPProviderWithClient creates a GenericHTTPProvider with a custom HTTP client (for testing).
func newGenericHTTPProviderWithClient(client httpDoer, baseURL, apiKey, model, embeddingModel string, logger *slog.Logger) *GenericHTTPProvider {
	return &GenericHTTPProvider{
		client:         client,
		baseURL:        baseURL,
		apiKey:         apiKey,
		model:          model,
		embeddingModel: embeddingModel,
		logger:         logger,
	}
}

// GenerateContent sends a prompt to the chat completions endpoint and returns the generated text.
func (p *GenericHTTPProvider) GenerateContent(prompt string) (string, error) {
	p.logger.Debug("GenerateContent request",
		"model", p.model,
		"prompt", prompt,
	)
	start := time.Now()

	reqBody := chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
	}

	var resp chatResponse
	err := retryOnRateLimit(p.logger, func(ctx context.Context) error {
		return p.doChat(ctx, reqBody, &resp)
	})
	if err != nil {
		p.logger.Debug("GenerateContent failed",
			"model", p.model,
			"error", err,
			"duration", time.Since(start),
		)
		return "", fmt.Errorf("generating content: %w", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("generating content: empty response")
	}

	text := resp.Choices[0].Message.Content
	p.logger.Debug("GenerateContent response",
		"model", p.model,
		"response", text,
		"duration", time.Since(start),
	)
	return text, nil
}

// GenerateJSON sends a prompt with JSON mode enabled and returns the generated JSON string.
// The schema is embedded in the prompt text as guidance.
func (p *GenericHTTPProvider) GenerateJSON(prompt string, schema string) (string, error) {
	p.logger.Debug("GenerateJSON request",
		"model", p.model,
		"prompt", prompt,
		"schema", schema,
	)
	start := time.Now()

	fullPrompt := prompt + "\n\nRespond with a JSON object following this exact schema:\n" + schema

	reqBody := chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "user", Content: fullPrompt},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var resp chatResponse
	err := retryOnRateLimit(p.logger, func(ctx context.Context) error {
		return p.doChat(ctx, reqBody, &resp)
	})
	if err != nil {
		p.logger.Debug("GenerateJSON failed",
			"model", p.model,
			"error", err,
			"duration", time.Since(start),
		)
		return "", fmt.Errorf("generating JSON content: %w", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("generating JSON content: empty response")
	}

	text := resp.Choices[0].Message.Content
	p.logger.Debug("GenerateJSON response",
		"model", p.model,
		"response", text,
		"duration", time.Since(start),
	)
	return text, nil
}

// EmbedContent sends text to the embeddings endpoint and returns the embedding vector.
func (p *GenericHTTPProvider) EmbedContent(text string) ([]float32, error) {
	p.logger.Debug("EmbedContent request",
		"model", p.embeddingModel,
		"text", text,
	)
	start := time.Now()

	reqBody := embeddingRequest{
		Model: p.embeddingModel,
		Input: text,
	}

	var resp embeddingResponse
	err := retryOnRateLimit(p.logger, func(ctx context.Context) error {
		return p.doEmbedding(ctx, reqBody, &resp)
	})
	if err != nil {
		p.logger.Debug("EmbedContent failed",
			"model", p.embeddingModel,
			"error", err,
			"duration", time.Since(start),
		)
		return nil, fmt.Errorf("embedding content: %w", err)
	}

	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding content: empty response")
	}

	p.logger.Debug("EmbedContent response",
		"model", p.embeddingModel,
		"vector_dim", len(resp.Data[0].Embedding),
		"duration", time.Since(start),
	)
	return resp.Data[0].Embedding, nil
}

// DetectVectorSize returns the embedding dimensionality by generating a test embedding.
func (p *GenericHTTPProvider) DetectVectorSize() (int, error) {
	vec, err := p.EmbedContent("test")
	if err != nil {
		return 0, fmt.Errorf("detect vector size: %w", err)
	}
	return len(vec), nil
}

// doChat sends a chat completions request and decodes the response.
func (p *GenericHTTPProvider) doChat(ctx context.Context, reqBody chatRequest, result *chatResponse) error {
	return p.doRequest(ctx, "/chat/completions", reqBody, result)
}

// doEmbedding sends an embeddings request and decodes the response.
func (p *GenericHTTPProvider) doEmbedding(ctx context.Context, reqBody embeddingRequest, result *embeddingResponse) error {
	return p.doRequest(ctx, "/embeddings", reqBody, result)
}

// doRequest sends an HTTP POST request and decodes the JSON response.
func (p *GenericHTTPProvider) doRequest(ctx context.Context, path string, reqBody any, result any) error {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &HTTPError{StatusCode: resp.StatusCode, Body: fmt.Sprintf("%d: %s", resp.StatusCode, string(body))}
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
