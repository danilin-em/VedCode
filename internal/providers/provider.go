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

// NewTextGenerator creates a TextGenerator based on provider config.
func NewTextGenerator(cfg config.ProviderConfig) (TextGenerator, error) {
	switch cfg.Provider {
	case "gemini":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("gemini provider requires api_key")
		}
		return NewGeminiProvider(cfg.APIKey, cfg.Model, "")
	default:
		return nil, fmt.Errorf("unsupported llm.provider: %q (supported: gemini)", cfg.Provider)
	}
}

// NewEmbeddingProvider creates an EmbeddingProvider based on provider config.
func NewEmbeddingProvider(cfg config.ProviderConfig) (EmbeddingProvider, error) {
	switch cfg.Provider {
	case "gemini":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("gemini provider requires api_key")
		}
		return NewGeminiProvider(cfg.APIKey, "", cfg.Model)
	default:
		return nil, fmt.Errorf("unsupported embedding.provider: %q (supported: gemini)", cfg.Provider)
	}
}
