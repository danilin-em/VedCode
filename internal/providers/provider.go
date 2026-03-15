package providers

import (
	"fmt"
	"log/slog"

	"VedCode/internal/config"
)

// TextGenerator generates text from a prompt.
type TextGenerator interface {
	GenerateContent(prompt string) (string, error)
	GenerateJSON(prompt string, schema string) (string, error)
}

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	EmbedContent(text string) ([]float32, error)
	// DetectVectorSize returns the embedding vector dimensionality
	// by generating a test embedding.
	DetectVectorSize() (int, error)
}

// NewTextGenerator creates a TextGenerator based on provider config.
func NewTextGenerator(cfg config.ProviderConfig, logger *slog.Logger) (TextGenerator, error) {
	switch cfg.Provider {
	case "gemini":
		return NewGeminiProvider(cfg.APIKey, cfg.Model, "", logger)
	case "generic-http":
		return NewGenericHTTPProvider(cfg.URL, cfg.APIKey, cfg.Model, "", logger), nil
	default:
		return nil, fmt.Errorf("unsupported llm.provider: %q (supported: gemini, generic-http)", cfg.Provider)
	}
}

// NewEmbeddingProvider creates an EmbeddingProvider based on provider config.
func NewEmbeddingProvider(cfg config.ProviderConfig, logger *slog.Logger) (EmbeddingProvider, error) {
	switch cfg.Provider {
	case "gemini":
		return NewGeminiProvider(cfg.APIKey, "", cfg.Model, logger)
	case "generic-http":
		return NewGenericHTTPProvider(cfg.URL, cfg.APIKey, "", cfg.Model, logger), nil
	default:
		return nil, fmt.Errorf("unsupported embedding.provider: %q (supported: gemini, generic-http)", cfg.Provider)
	}
}
