package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".vedcode.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `
project:
  name: "my-app"
  root_path: "."

indexer:
  max_file_size: 2097152
  ignore_patterns:
    - "*.min.js"
    - "*.map"

llm:
  provider: "gemini"
  api_key: "test-key-123"
  model: "gemini-2.5-flash"
  embedding_model: "gemini-embedding-001"

storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "vedcode_"
`

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTestConfig(t, validConfig)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "my-app" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "my-app")
	}
	if cfg.Project.RootPath != "." {
		t.Errorf("project.root_path = %q, want %q", cfg.Project.RootPath, ".")
	}
	if cfg.Indexer.MaxFileSize != 2097152 {
		t.Errorf("indexer.max_file_size = %d, want %d", cfg.Indexer.MaxFileSize, 2097152)
	}
	if len(cfg.Indexer.IgnorePatterns) != 2 {
		t.Errorf("indexer.ignore_patterns length = %d, want %d", len(cfg.Indexer.IgnorePatterns), 2)
	}
	if cfg.LLM.Provider != "gemini" {
		t.Errorf("llm.provider = %q, want %q", cfg.LLM.Provider, "gemini")
	}
	if cfg.LLM.APIKey != "test-key-123" {
		t.Errorf("llm.api_key = %q, want %q", cfg.LLM.APIKey, "test-key-123")
	}
	if cfg.LLM.Model != "gemini-2.5-flash" {
		t.Errorf("llm.model = %q, want %q", cfg.LLM.Model, "gemini-2.5-flash")
	}
	if cfg.LLM.EmbeddingModel != "gemini-embedding-001" {
		t.Errorf("llm.embedding_model = %q, want %q", cfg.LLM.EmbeddingModel, "gemini-embedding-001")
	}
	if cfg.Storage.Type != "qdrant" {
		t.Errorf("storage.type = %q, want %q", cfg.Storage.Type, "qdrant")
	}
	if cfg.Storage.URL != "http://localhost:6333" {
		t.Errorf("storage.url = %q, want %q", cfg.Storage.URL, "http://localhost:6333")
	}
	if cfg.Storage.CollectionPrefix != "vedcode_" {
		t.Errorf("storage.collection_prefix = %q, want %q", cfg.Storage.CollectionPrefix, "vedcode_")
	}
}

func TestLoad_DefaultMaxFileSize(t *testing.T) {
	yml := `
project:
  name: "app"
  root_path: "."
llm:
  provider: "gemini"
  api_key: "key"
  model: "model"
  embedding_model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Indexer.MaxFileSize != DefaultMaxFileSize {
		t.Errorf("default max_file_size = %d, want %d", cfg.Indexer.MaxFileSize, DefaultMaxFileSize)
	}
	if cfg.Indexer.Workers != 2 {
		t.Errorf("default workers = %d, want %d", cfg.Indexer.Workers, 2)
	}
}

func TestLoad_EnvVarSubstitution(t *testing.T) {
	t.Setenv("TEST_VEDCODE_API_KEY", "secret-from-env")

	yml := `
project:
  name: "app"
  root_path: "."
llm:
  provider: "gemini"
  api_key: "${TEST_VEDCODE_API_KEY}"
  model: "model"
  embedding_model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "secret-from-env" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "secret-from-env")
	}
}

func TestLoad_EnvVarNotSet(t *testing.T) {
	os.Unsetenv("NONEXISTENT_VAR_VEDCODE")

	yml := `
project:
  name: "app"
  root_path: "."
llm:
  provider: "gemini"
  api_key: "${NONEXISTENT_VAR_VEDCODE}"
  model: "model"
  embedding_model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When env var is not set, the placeholder remains as-is
	if cfg.LLM.APIKey != "${NONEXISTENT_VAR_VEDCODE}" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "${NONEXISTENT_VAR_VEDCODE}")
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yml     string
		wantErr string
	}{
		{
			name: "missing project.name",
			yml: `
project:
  root_path: "."
llm:
  provider: "g"
  api_key: "k"
  model: "m"
  embedding_model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "project.name is required",
		},
		{
			name: "missing project.root_path",
			yml: `
project:
  name: "app"
llm:
  provider: "g"
  api_key: "k"
  model: "m"
  embedding_model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "project.root_path is required",
		},
		{
			name: "missing llm.provider",
			yml: `
project:
  name: "app"
  root_path: "."
llm:
  api_key: "k"
  model: "m"
  embedding_model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "llm.provider is required",
		},
		{
			name: "missing llm.api_key",
			yml: `
project:
  name: "app"
  root_path: "."
llm:
  provider: "g"
  model: "m"
  embedding_model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "llm.api_key is required",
		},
		{
			name: "missing storage.url",
			yml: `
project:
  name: "app"
  root_path: "."
llm:
  provider: "g"
  api_key: "k"
  model: "m"
  embedding_model: "e"
storage:
  type: "q"
  collection_prefix: "p"
`,
			wantErr: "storage.url is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestConfig(t, tt.yml)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); got != "config validation: "+tt.wantErr {
				t.Errorf("error = %q, want to contain %q", got, tt.wantErr)
			}
		})
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/.vedcode.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTestConfig(t, "{{invalid yaml}}")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
