package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"VedCode/internal/prompts"
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
indexer:
  max_file_size: 2097152
  ignore_patterns:
    - "*.min.js"
    - "*.map"

llm:
  provider: "gemini"
  api_key: "test-key-123"
  model: "gemini-2.5-flash"

embedding:
  provider: "gemini"
  api_key: "test-key-123"
  model: "gemini-embedding-001"

storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "vedcode_"
`

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTestConfig(t, validConfig)

	cfg, err := loadWithPaths("", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name == "" {
		t.Error("project.name should be computed from cwd, got empty")
	}
	if cfg.Project.RootPath == "" {
		t.Error("project.root_path should be computed from cwd, got empty")
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
	if cfg.Embedding.Provider != "gemini" {
		t.Errorf("embedding.provider = %q, want %q", cfg.Embedding.Provider, "gemini")
	}
	if cfg.Embedding.Model != "gemini-embedding-001" {
		t.Errorf("embedding.model = %q, want %q", cfg.Embedding.Model, "gemini-embedding-001")
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
llm:
  provider: "gemini"
  api_key: "key"
  model: "model"
embedding:
  provider: "gemini"
  api_key: "key"
  model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := loadWithPaths("", path)
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
llm:
  provider: "gemini"
  api_key: "${TEST_VEDCODE_API_KEY}"
  model: "model"
embedding:
  provider: "gemini"
  api_key: "${TEST_VEDCODE_API_KEY}"
  model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := loadWithPaths("", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "secret-from-env" {
		t.Errorf("llm.api_key = %q, want %q", cfg.LLM.APIKey, "secret-from-env")
	}
	if cfg.Embedding.APIKey != "secret-from-env" {
		t.Errorf("embedding.api_key = %q, want %q", cfg.Embedding.APIKey, "secret-from-env")
	}
}

func TestLoad_EnvVarNotSet(t *testing.T) {
	os.Unsetenv("NONEXISTENT_VAR_VEDCODE")

	yml := `
llm:
  provider: "gemini"
  api_key: "${NONEXISTENT_VAR_VEDCODE}"
  model: "model"
embedding:
  provider: "gemini"
  api_key: "key"
  model: "emb"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := loadWithPaths("", path)
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
			name: "missing llm.provider",
			yml: `
llm:
  api_key: "k"
  model: "m"
embedding:
  provider: "g"
  model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "llm.provider is required",
		},
		{
			name: "missing embedding.provider",
			yml: `
llm:
  provider: "g"
  api_key: "k"
  model: "m"
embedding:
  model: "e"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "embedding.provider is required",
		},
		{
			name: "missing embedding.model",
			yml: `
llm:
  provider: "g"
  api_key: "k"
  model: "m"
embedding:
  provider: "g"
storage:
  type: "q"
  url: "http://x"
  collection_prefix: "p"
`,
			wantErr: "embedding.model is required",
		},
		{
			name: "missing storage.url",
			yml: `
llm:
  provider: "g"
  api_key: "k"
  model: "m"
embedding:
  provider: "g"
  model: "e"
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
			_, err := loadWithPaths("", path)
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
	_, err := loadWithPaths("", "/nonexistent/path/.vedcode.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTestConfig(t, "{{invalid yaml}}")
	_, err := loadWithPaths("", path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestPathToName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/evgenii/Projects/VedCode", "home-evgenii-Projects-VedCode"},
		{"/usr/local/src", "usr-local-src"},
		{"/single", "single"},
	}
	for _, tt := range tests {
		got := pathToName(tt.path)
		if got != tt.want {
			t.Errorf("pathToName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// --- Tests for home/project config merging ---

const homeConfig = `
indexer:
  max_file_size: 1048576
  workers: 4
  ignore_patterns:
    - "*.log"
    - "node_modules/*"

llm:
  provider: "gemini"
  api_key: "home-api-key"
  model: "gemini-2.5-flash"

embedding:
  provider: "gemini"
  api_key: "home-api-key"
  model: "gemini-embedding-001"

storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "vedcode_"
`

func TestLoad_HomeConfigOnly(t *testing.T) {
	homePath := writeTestConfig(t, homeConfig)

	cfg, err := loadWithPaths(homePath, "/nonexistent/project/.vedcode.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "home-api-key" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "home-api-key")
	}
	if cfg.Indexer.Workers != 4 {
		t.Errorf("workers = %d, want %d", cfg.Indexer.Workers, 4)
	}
}

func TestLoad_ProjectConfigOnly(t *testing.T) {
	projectPath := writeTestConfig(t, validConfig)

	cfg, err := loadWithPaths("/nonexistent/home/.vedcode.yml", projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "test-key-123" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "test-key-123")
	}
}

func TestLoad_BothConfigs_ProjectOverridesHome(t *testing.T) {
	homePath := writeTestConfig(t, homeConfig)

	projectYml := `
indexer:
  workers: 8

llm:
  model: "gemini-2.0-pro"
`
	projectPath := writeTestConfig(t, projectYml)

	cfg, err := loadWithPaths(homePath, projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Overridden by project
	if cfg.Indexer.Workers != 8 {
		t.Errorf("workers = %d, want %d", cfg.Indexer.Workers, 8)
	}
	if cfg.LLM.Model != "gemini-2.0-pro" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "gemini-2.0-pro")
	}

	// Inherited from home
	if cfg.LLM.APIKey != "home-api-key" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "home-api-key")
	}
	if cfg.LLM.Provider != "gemini" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "gemini")
	}
	if cfg.Storage.URL != "http://localhost:6333" {
		t.Errorf("storage.url = %q, want %q", cfg.Storage.URL, "http://localhost:6333")
	}
}

func TestLoad_IgnorePatternsAppended(t *testing.T) {
	homePath := writeTestConfig(t, homeConfig)

	projectYml := `
indexer:
  ignore_patterns:
    - "*.min.js"
    - "dist/*"
`
	projectPath := writeTestConfig(t, projectYml)

	cfg, err := loadWithPaths(homePath, projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Home patterns: *.log, node_modules/*
	// Project patterns: *.min.js, dist/*
	// Result: all 4 appended
	want := []string{"*.log", "node_modules/*", "*.min.js", "dist/*"}
	if len(cfg.Indexer.IgnorePatterns) != len(want) {
		t.Fatalf("ignore_patterns length = %d, want %d: %v", len(cfg.Indexer.IgnorePatterns), len(want), cfg.Indexer.IgnorePatterns)
	}
	for i, p := range want {
		if cfg.Indexer.IgnorePatterns[i] != p {
			t.Errorf("ignore_patterns[%d] = %q, want %q", i, cfg.Indexer.IgnorePatterns[i], p)
		}
	}
}

func TestLoad_DifferentLLMAndEmbeddingProviders(t *testing.T) {
	yml := `
llm:
  provider: "gemini"
  api_key: "key"
  model: "gemini-2.5-flash"
embedding:
  provider: "ollama"
  url: "http://localhost:11434"
  model: "nomic-embed-text"
storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "v_"
`
	path := writeTestConfig(t, yml)

	cfg, err := loadWithPaths("", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Provider != "gemini" {
		t.Errorf("llm.provider = %q, want %q", cfg.LLM.Provider, "gemini")
	}
	if cfg.Embedding.Provider != "ollama" {
		t.Errorf("embedding.provider = %q, want %q", cfg.Embedding.Provider, "ollama")
	}
	if cfg.Embedding.URL != "http://localhost:11434" {
		t.Errorf("embedding.url = %q, want %q", cfg.Embedding.URL, "http://localhost:11434")
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Errorf("embedding.model = %q, want %q", cfg.Embedding.Model, "nomic-embed-text")
	}
}

func TestLoad_EmbeddingMerge_ProjectOverridesHome(t *testing.T) {
	homePath := writeTestConfig(t, homeConfig)

	projectYml := `
embedding:
  provider: "ollama"
  url: "http://localhost:11434"
  model: "project-model"
`
	projectPath := writeTestConfig(t, projectYml)

	cfg, err := loadWithPaths(homePath, projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Embedding.Provider != "ollama" {
		t.Errorf("embedding.provider = %q, want %q", cfg.Embedding.Provider, "ollama")
	}
	if cfg.Embedding.Model != "project-model" {
		t.Errorf("embedding.model = %q, want %q", cfg.Embedding.Model, "project-model")
	}
}

func TestLoad_NeitherConfig(t *testing.T) {
	_, err := loadWithPaths("/nonexistent/home/.vedcode.yml", "/nonexistent/project/.vedcode.yml")
	if err == nil {
		t.Fatal("expected error when neither config exists")
	}
	if !strings.Contains(err.Error(), "no config found") {
		t.Errorf("error = %q, want to contain 'no config found'", err.Error())
	}
}

// --- Tests for prompts config ---

func TestLoad_PromptsDefaultsWhenEmpty(t *testing.T) {
	path := writeTestConfig(t, validConfig)

	cfg, err := loadWithPaths("", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Prompts.ProjectStructureAnalysis != prompts.DefaultProjectStructureAnalysis {
		t.Error("expected default ProjectStructureAnalysis prompt")
	}
	if cfg.Prompts.SourceCodeAnalysis != prompts.DefaultSourceCodeAnalysis {
		t.Error("expected default SourceCodeAnalysis prompt")
	}
	if cfg.Prompts.DirectoryAnalysis != prompts.DefaultDirectoryAnalysis {
		t.Error("expected default DirectoryAnalysis prompt")
	}
}

func TestLoad_PromptsFromConfig(t *testing.T) {
	yml := validConfig + `
prompts:
  project_structure_analysis: "Custom structure: ${CONTENT}"
  source_code_analysis: "Custom code: ${CONTENT}"
  directory_analysis: "Custom dir: ${DIR_PATH}"
`
	path := writeTestConfig(t, yml)

	cfg, err := loadWithPaths("", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Prompts.ProjectStructureAnalysis != "Custom structure: ${CONTENT}" {
		t.Errorf("project_structure_analysis = %q, want custom", cfg.Prompts.ProjectStructureAnalysis)
	}
	if cfg.Prompts.SourceCodeAnalysis != "Custom code: ${CONTENT}" {
		t.Errorf("source_code_analysis = %q, want custom", cfg.Prompts.SourceCodeAnalysis)
	}
	if cfg.Prompts.DirectoryAnalysis != "Custom dir: ${DIR_PATH}" {
		t.Errorf("directory_analysis = %q, want custom", cfg.Prompts.DirectoryAnalysis)
	}
}

func TestLoad_PromptsMerge_ProjectOverridesHome(t *testing.T) {
	homeYml := homeConfig + `
prompts:
  project_structure_analysis: "home-structure"
  source_code_analysis: "home-code"
  directory_analysis: "home-dir"
`
	homePath := writeTestConfig(t, homeYml)

	projectYml := `
prompts:
  source_code_analysis: "project-code"
`
	projectPath := writeTestConfig(t, projectYml)

	cfg, err := loadWithPaths(homePath, projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Prompts.SourceCodeAnalysis != "project-code" {
		t.Errorf("source_code_analysis = %q, want %q", cfg.Prompts.SourceCodeAnalysis, "project-code")
	}
	if cfg.Prompts.ProjectStructureAnalysis != "home-structure" {
		t.Errorf("project_structure_analysis = %q, want %q", cfg.Prompts.ProjectStructureAnalysis, "home-structure")
	}
	if cfg.Prompts.DirectoryAnalysis != "home-dir" {
		t.Errorf("directory_analysis = %q, want %q", cfg.Prompts.DirectoryAnalysis, "home-dir")
	}
}
