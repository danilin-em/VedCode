package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"VedCode/internal/prompts"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project   ProjectConfig  `yaml:"-"`
	Indexer   IndexerConfig  `yaml:"indexer"`
	LLM       ProviderConfig `yaml:"llm"`
	Embedding ProviderConfig `yaml:"embedding"`
	Storage   StorageConfig  `yaml:"storage"`
	Prompts   PromptsConfig  `yaml:"prompts"`
}

type ProjectConfig struct {
	Name     string `yaml:"name"`
	RootPath string `yaml:"root_path"`
}

type IndexerConfig struct {
	MaxFileSize    int64    `yaml:"max_file_size"`
	IgnorePatterns []string `yaml:"ignore_patterns"`
	Workers        int      `yaml:"workers"`
	TreeFileDepth  int      `yaml:"tree_file_depth"`
}

type ProviderConfig struct {
	Provider   string `yaml:"provider"`
	APIKey     string `yaml:"api_key"`
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	VectorSize int    `yaml:"vector_size"`
}

type StorageConfig struct {
	Type             string `yaml:"type"`
	URL              string `yaml:"url"`
	CollectionPrefix string `yaml:"collection_prefix"`
}

type PromptsConfig struct {
	ProjectStructureAnalysis string `yaml:"project_structure_analysis"`
	SourceCodeAnalysis       string `yaml:"source_code_analysis"`
	DirectoryAnalysis        string `yaml:"directory_analysis"`
	EnrichedOverviewAnalysis string `yaml:"enriched_overview_analysis"`
}

const DefaultMaxFileSize = 1048576 // 1 MB
const DefaultTreeFileDepth = 3

var envVarRegexp = regexp.MustCompile(`\$\{(\w+)\}`)

// Load reads ~/.vedcode.yml (global defaults) and the project config,
// merges them (project overrides home; ignore_patterns are appended),
// sets defaults, and validates.
func Load(projectPath string) (*Config, error) {
	homePath := ""
	if dir, err := os.UserHomeDir(); err == nil {
		homePath = filepath.Join(dir, ".vedcode.yml")
	}
	return loadWithPaths(homePath, projectPath)
}

func loadWithPaths(homePath, projectPath string) (*Config, error) {
	homeCfg, homeErr := loadFile(homePath)
	projectCfg, projectErr := loadFile(projectPath)

	// If both files are missing, return a clear error
	if homeCfg == nil && projectCfg == nil {
		if projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
			return nil, projectErr
		}
		if homeErr != nil && !errors.Is(homeErr, os.ErrNotExist) {
			return nil, homeErr
		}
		return nil, fmt.Errorf("no config found: create ~/.vedcode.yml or %s", projectPath)
	}

	// If one file had a parse error (not just missing), report it
	if homeCfg == nil && homeErr != nil && !errors.Is(homeErr, os.ErrNotExist) {
		return nil, homeErr
	}
	if projectCfg == nil && projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
		return nil, projectErr
	}

	var cfg *Config
	switch {
	case homeCfg != nil && projectCfg != nil:
		cfg = merge(homeCfg, projectCfg)
	case homeCfg != nil:
		cfg = homeCfg
	default:
		cfg = projectCfg
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	cfg.Project.RootPath = cwd
	cfg.Project.Name = pathToName(cwd)

	setDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadFile reads a single YAML config file with env var substitution.
// Returns (nil, os.ErrNotExist) if file doesn't exist.
func loadFile(path string) (*Config, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	expanded := expandEnvVars(string(data))
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return &cfg, nil
}

// merge combines home and project configs.
// Project values override home values; ignore_patterns are appended.
func merge(home, project *Config) *Config {
	cfg := *home

	// LLM: override non-zero fields
	if project.LLM.Provider != "" {
		cfg.LLM.Provider = project.LLM.Provider
	}
	if project.LLM.APIKey != "" {
		cfg.LLM.APIKey = project.LLM.APIKey
	}
	if project.LLM.Model != "" {
		cfg.LLM.Model = project.LLM.Model
	}
	if project.LLM.URL != "" {
		cfg.LLM.URL = project.LLM.URL
	}

	// Embedding: override non-zero fields
	if project.Embedding.Provider != "" {
		cfg.Embedding.Provider = project.Embedding.Provider
	}
	if project.Embedding.APIKey != "" {
		cfg.Embedding.APIKey = project.Embedding.APIKey
	}
	if project.Embedding.URL != "" {
		cfg.Embedding.URL = project.Embedding.URL
	}
	if project.Embedding.Model != "" {
		cfg.Embedding.Model = project.Embedding.Model
	}
	if project.Embedding.VectorSize != 0 {
		cfg.Embedding.VectorSize = project.Embedding.VectorSize
	}

	// Storage: override non-zero fields
	if project.Storage.Type != "" {
		cfg.Storage.Type = project.Storage.Type
	}
	if project.Storage.URL != "" {
		cfg.Storage.URL = project.Storage.URL
	}
	if project.Storage.CollectionPrefix != "" {
		cfg.Storage.CollectionPrefix = project.Storage.CollectionPrefix
	}
	// Indexer: override non-zero fields
	if project.Indexer.MaxFileSize != 0 {
		cfg.Indexer.MaxFileSize = project.Indexer.MaxFileSize
	}
	if project.Indexer.Workers != 0 {
		cfg.Indexer.Workers = project.Indexer.Workers
	}
	if project.Indexer.TreeFileDepth != 0 {
		cfg.Indexer.TreeFileDepth = project.Indexer.TreeFileDepth
	}

	// IgnorePatterns: append project patterns to home patterns
	if len(project.Indexer.IgnorePatterns) > 0 {
		cfg.Indexer.IgnorePatterns = append(cfg.Indexer.IgnorePatterns, project.Indexer.IgnorePatterns...)
	}

	// Prompts: override non-zero fields
	if project.Prompts.ProjectStructureAnalysis != "" {
		cfg.Prompts.ProjectStructureAnalysis = project.Prompts.ProjectStructureAnalysis
	}
	if project.Prompts.SourceCodeAnalysis != "" {
		cfg.Prompts.SourceCodeAnalysis = project.Prompts.SourceCodeAnalysis
	}
	if project.Prompts.DirectoryAnalysis != "" {
		cfg.Prompts.DirectoryAnalysis = project.Prompts.DirectoryAnalysis
	}
	if project.Prompts.EnrichedOverviewAnalysis != "" {
		cfg.Prompts.EnrichedOverviewAnalysis = project.Prompts.EnrichedOverviewAnalysis
	}

	return &cfg
}

// expandEnvVars replaces ${VAR_NAME} patterns with environment variable values.
func expandEnvVars(s string) string {
	return envVarRegexp.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarRegexp.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

func setDefaults(cfg *Config) {
	if cfg.Indexer.MaxFileSize == 0 {
		cfg.Indexer.MaxFileSize = DefaultMaxFileSize
	}
	if cfg.Indexer.Workers <= 0 {
		cfg.Indexer.Workers = 2
	}
	if cfg.Indexer.TreeFileDepth <= 0 {
		cfg.Indexer.TreeFileDepth = DefaultTreeFileDepth
	}
	if cfg.Prompts.ProjectStructureAnalysis == "" {
		cfg.Prompts.ProjectStructureAnalysis = prompts.DefaultProjectStructureAnalysis
	}
	if cfg.Prompts.SourceCodeAnalysis == "" {
		cfg.Prompts.SourceCodeAnalysis = prompts.DefaultSourceCodeAnalysis
	}
	if cfg.Prompts.DirectoryAnalysis == "" {
		cfg.Prompts.DirectoryAnalysis = prompts.DefaultDirectoryAnalysis
	}
	if cfg.Prompts.EnrichedOverviewAnalysis == "" {
		cfg.Prompts.EnrichedOverviewAnalysis = prompts.DefaultEnrichedOverviewAnalysis
	}
}

// pathToName converts an absolute path to a safe identifier.
// e.g. "/home/evgenii/Projects/VedCode" → "home-evgenii-Projects-VedCode"
func pathToName(absPath string) string {
	name := strings.TrimPrefix(absPath, "/")
	return strings.ReplaceAll(name, "/", "-")
}

func validate(cfg *Config) error {
	if cfg.LLM.Provider == "" {
		return fmt.Errorf("config validation: llm.provider is required")
	}
	if cfg.LLM.Model == "" {
		return fmt.Errorf("config validation: llm.model is required")
	}
	if cfg.Embedding.Provider == "" {
		return fmt.Errorf("config validation: embedding.provider is required")
	}
	if cfg.Embedding.Model == "" {
		return fmt.Errorf("config validation: embedding.model is required")
	}
	if cfg.Storage.Type == "" {
		return fmt.Errorf("config validation: storage.type is required")
	}
	if cfg.Storage.URL == "" {
		return fmt.Errorf("config validation: storage.url is required")
	}
	if cfg.Storage.CollectionPrefix == "" {
		return fmt.Errorf("config validation: storage.collection_prefix is required")
	}
	// URL is required only for HTTP-based providers
	if requiresURL(cfg.LLM.Provider) && cfg.LLM.URL == "" {
		return fmt.Errorf("config validation: llm.url is required for provider %q", cfg.LLM.Provider)
	}
	if requiresURL(cfg.Embedding.Provider) && cfg.Embedding.URL == "" {
		return fmt.Errorf("config validation: embedding.url is required for provider %q", cfg.Embedding.Provider)
	}

	// API key is required for SDK-based providers
	if cfg.LLM.Provider == "gemini" && cfg.LLM.APIKey == "" {
		return fmt.Errorf("config validation: llm.api_key is required for gemini provider")
	}
	if cfg.Embedding.Provider == "gemini" && cfg.Embedding.APIKey == "" {
		return fmt.Errorf("config validation: embedding.api_key is required for gemini provider")
	}

	return nil
}

// requiresURL returns true for HTTP-based providers that need a base URL.
func requiresURL(provider string) bool {
	switch provider {
	case "generic-http":
		return true
	default:
		return false
	}
}
