package providers

import (
	"fmt"

	"VedCode/internal/config"
)

// TextGenerator generates text from a prompt.
type TextGenerator interface {
	GenerateContent(prompt string) (string, error)
}

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	EmbedContent(text string) ([]float32, error)
}

// Provider combines text generation and embedding capabilities.
type Provider interface {
	TextGenerator
	EmbeddingProvider
}

// New creates a Provider based on the LLM config.
func New(cfg config.LLMConfig) (Provider, error) {
	switch cfg.Provider {
	case "gemini":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("gemini provider requires llm.api_key")
		}
		return NewGeminiProvider(cfg.APIKey, cfg.Model, cfg.EmbeddingModel)
	default:
		return nil, fmt.Errorf("unsupported llm.provider: %q (supported: gemini)", cfg.Provider)
	}
}
