package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project ProjectConfig `yaml:"project"`
	Indexer IndexerConfig `yaml:"indexer"`
	LLM     LLMConfig     `yaml:"llm"`
	Storage StorageConfig `yaml:"storage"`
}

type ProjectConfig struct {
	Name     string `yaml:"name"`
	RootPath string `yaml:"root_path"`
}

type IndexerConfig struct {
	MaxFileSize    int64    `yaml:"max_file_size"`
	IgnorePatterns []string `yaml:"ignore_patterns"`
	Workers        int      `yaml:"workers"`
}

type LLMConfig struct {
	Provider       string `yaml:"provider"`
	APIKey         string `yaml:"api_key"`
	Model          string `yaml:"model"`
	EmbeddingModel string `yaml:"embedding_model"`
}

type StorageConfig struct {
	Type             string `yaml:"type"`
	URL              string `yaml:"url"`
	CollectionPrefix string `yaml:"collection_prefix"`
}

const DefaultMaxFileSize = 1048576 // 1 MB

var envVarRegexp = regexp.MustCompile(`\$\{(\w+)\}`)

// Load reads and parses .vedcode.yml from the given path,
// substitutes environment variables, sets defaults, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	setDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
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
}

func validate(cfg *Config) error {
	if cfg.Project.Name == "" {
		return fmt.Errorf("config validation: project.name is required")
	}
	if cfg.Project.RootPath == "" {
		return fmt.Errorf("config validation: project.root_path is required")
	}
	if cfg.LLM.Provider == "" {
		return fmt.Errorf("config validation: llm.provider is required")
	}
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("config validation: llm.api_key is required")
	}
	if cfg.LLM.Model == "" {
		return fmt.Errorf("config validation: llm.model is required")
	}
	if cfg.LLM.EmbeddingModel == "" {
		return fmt.Errorf("config validation: llm.embedding_model is required")
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
	return nil
}
