package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key-123",
		"host": "0.0.0.0",
		"port": 8080,
		"opencode_go": {
			"base_url": "https://custom.url/v1",
			"timeout_ms": 60000
		},
		"logging": {
			"level": "debug",
			"requests": true
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	// Prevent env var API key from overriding test config
	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.APIKey != "test-key-123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "test-key-123")
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if cfg.OpenCodeGo.BaseURL != "https://custom.url/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.OpenCodeGo.BaseURL, "https://custom.url/v1")
	}
	if cfg.OpenCodeGo.TimeoutMs != 60000 {
		t.Errorf("TimeoutMs = %d, want %d", cfg.OpenCodeGo.TimeoutMs, 60000)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.Logging.Level, "debug")
	}
	if !cfg.Logging.Requests {
		t.Error("Logging.Requests = false, want true")
	}
}

func TestLoadMissingAPIKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{"host": "127.0.0.1"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	// Prevent env var API key from making this test pass incorrectly
	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for missing API key, got nil")
	}
}

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{"api_key": "file-key"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	_ = os.Setenv("OC_GO_CC_API_KEY", "env-key")
	_ = os.Setenv("OC_GO_CC_HOST", "env-host")
	_ = os.Setenv("OC_GO_CC_PORT", "9999")
	_ = os.Setenv("OC_GO_CC_OPENCODE_URL", "https://env-url/v1")
	_ = os.Setenv("OC_GO_CC_LOG_LEVEL", "warn")
	defer func() {
		_ = os.Unsetenv("OC_GO_CC_CONFIG")
		_ = os.Unsetenv("OC_GO_CC_API_KEY")
		_ = os.Unsetenv("OC_GO_CC_HOST")
		_ = os.Unsetenv("OC_GO_CC_PORT")
		_ = os.Unsetenv("OC_GO_CC_OPENCODE_URL")
		_ = os.Unsetenv("OC_GO_CC_LOG_LEVEL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "env-key")
	}
	if cfg.Host != "env-host" {
		t.Errorf("Host = %q, want %q", cfg.Host, "env-host")
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9999)
	}
	if cfg.OpenCodeGo.BaseURL != "https://env-url/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.OpenCodeGo.BaseURL, "https://env-url/v1")
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("LogLevel = %q, want %q", cfg.Logging.Level, "warn")
	}
}

func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Minimal config — only API key, everything else should default.
	cfgJSON := `{"api_key": "test-key"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Host != defaultHost {
		t.Errorf("Host = %q, want %q", cfg.Host, defaultHost)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.OpenCodeGo.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.OpenCodeGo.BaseURL, defaultBaseURL)
	}
	if cfg.OpenCodeGo.TimeoutMs != defaultTimeoutMs {
		t.Errorf("TimeoutMs = %d, want %d", cfg.OpenCodeGo.TimeoutMs, defaultTimeoutMs)
	}
	if cfg.Logging.Level != defaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", cfg.Logging.Level, defaultLogLevel)
	}
}

func TestLoadClaudeCodeConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key",
		"claude_code": {
			"models": {
				"haiku": {"provider": "opencode-go", "model_id": "qwen3.5-plus"},
				"sonnet": {"provider": "opencode-go", "model_id": "kimi-k2.6"},
				"opus": {"provider": "opencode-go", "model_id": "deepseek-v4-pro"}
			},
			"fallbacks": {
				"sonnet": [
					{"provider": "opencode-go", "model_id": "glm-5"}
				]
			}
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.ClaudeCode.Models["haiku"].ModelID; got != "qwen3.5-plus" {
		t.Errorf("ClaudeCode.Models[haiku].ModelID = %q, want %q", got, "qwen3.5-plus")
	}
	if got := cfg.ClaudeCode.Models["sonnet"].ModelID; got != "kimi-k2.6" {
		t.Errorf("ClaudeCode.Models[sonnet].ModelID = %q, want %q", got, "kimi-k2.6")
	}
	if got := len(cfg.ClaudeCode.Fallbacks["sonnet"]); got != 1 {
		t.Errorf("len(ClaudeCode.Fallbacks[sonnet]) = %d, want 1", got)
	}
}

func TestLoadRejectsInvalidClaudeCategory(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key",
		"claude_code": {
			"models": {
				"ultra": {"provider": "opencode-go", "model_id": "glm-5.1"}
			}
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid claude_code category, got nil")
	}
}

func TestLoadRejectsClaudeFallbackWithoutPrimary(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key",
		"claude_code": {
			"fallbacks": {
				"opus": [
					{"provider": "opencode-go", "model_id": "deepseek-v4-pro"}
				]
			}
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()

	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for claude_code fallback without primary model, got nil")
	}
}

func TestInterpolateEnvVars(t *testing.T) {
	_ = os.Setenv("TEST_SECRET", "my-secret-value")
	defer func() { _ = os.Unsetenv("TEST_SECRET") }()

	input := `{"api_key": "${TEST_SECRET}", "host": "${UNSET_VAR:-fallback}"}`
	result := interpolateEnvVars(input)

	want := `{"api_key": "my-secret-value", "host": "${UNSET_VAR:-fallback}"}`
	if result != want {
		t.Errorf("interpolateEnvVars() = %q, want %q", result, want)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	got := DefaultConfigPath()
	want := filepath.Join(DefaultConfigDir(), "config.json")
	if got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
}
