package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project ProjectConfig `yaml:"-"`
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

	// Compute project config from current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	cfg.Project.RootPath = cwd
	cfg.Project.Name = pathToName(cwd)

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
