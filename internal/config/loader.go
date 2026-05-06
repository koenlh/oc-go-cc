package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

const (
	defaultHost             = "127.0.0.1"
	defaultPort             = 3456
	defaultBaseURL          = "https://opencode.ai/zen/go/v1/chat/completions"
	defaultAnthropicBaseURL = "https://opencode.ai/zen/go/v1/messages"
	defaultTimeoutMs        = 300000
	defaultLogLevel         = "info"
)

// envVarPattern matches ${ENV_VAR} placeholders in config values.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// Load reads configuration from a JSON file and applies environment variable overrides.
// Config path resolution:
//  1. OC_GO_CC_CONFIG env var (explicit override)
//  2. platform default config path (default)
func Load() (*Config, error) {
	configPath := resolveConfigPath()

	cfg, err := loadJSON(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", configPath, err)
	}

	applyEnvOverrides(cfg)
	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// resolveConfigPath determines which config file to load.
func resolveConfigPath() string {
	if path := os.Getenv("OC_GO_CC_CONFIG"); path != "" {
		return path
	}
	return DefaultConfigPath()
}

// loadJSON reads and parses the configuration file.
func loadJSON(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Interpolate environment variables before parsing.
	data = []byte(interpolateEnvVars(string(data)))

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return &cfg, nil
}

// interpolateEnvVars replaces ${ENV_VAR} patterns with their actual values.
func interpolateEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name from ${VAR}
		varName := match[2 : len(match)-1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		// Leave unchanged if env var is not set
		return match
	})
}

// applyEnvOverrides applies environment variable overrides to the config.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("OC_GO_CC_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("OC_GO_CC_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("OC_GO_CC_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	}
	if v := os.Getenv("OC_GO_CC_OPENCODE_URL"); v != "" {
		cfg.OpenCodeGo.BaseURL = v
	}
	if v := os.Getenv("OC_GO_CC_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

// applyDefaults fills in missing configuration values with sensible defaults.
func applyDefaults(cfg *Config) {
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.OpenCodeGo.BaseURL == "" {
		cfg.OpenCodeGo.BaseURL = defaultBaseURL
	}
	if cfg.OpenCodeGo.AnthropicBaseURL == "" {
		cfg.OpenCodeGo.AnthropicBaseURL = defaultAnthropicBaseURL
	}
	if cfg.OpenCodeGo.TimeoutMs == 0 {
		cfg.OpenCodeGo.TimeoutMs = defaultTimeoutMs
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = defaultLogLevel
	}
}

// validate checks that all required configuration fields are present.
func validate(cfg *Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("api_key is required (set via config file or OC_GO_CC_API_KEY env var)")
	}
	if err := validateClaudeCodeConfig(cfg.ClaudeCode); err != nil {
		return err
	}
	return nil
}

func validateClaudeCodeConfig(cfg ClaudeCodeConfig) error {
	for category, model := range cfg.Models {
		if !IsValidClaudeCategory(category) {
			return fmt.Errorf("claude_code.models.%s is not supported (use haiku, sonnet, or opus)", category)
		}
		if model.ModelID == "" {
			return fmt.Errorf("claude_code.models.%s.model_id is required", category)
		}
	}

	for category, fallbacks := range cfg.Fallbacks {
		if !IsValidClaudeCategory(category) {
			return fmt.Errorf("claude_code.fallbacks.%s is not supported (use haiku, sonnet, or opus)", category)
		}
		if _, ok := cfg.Models[category]; !ok {
			return fmt.Errorf("claude_code.fallbacks.%s requires claude_code.models.%s to be configured", category, category)
		}
		for index, model := range fallbacks {
			if model.ModelID == "" {
				return fmt.Errorf("claude_code.fallbacks.%s[%d].model_id is required", category, index)
			}
		}
	}

	return nil
}
