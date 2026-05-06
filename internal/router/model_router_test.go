package router

import (
	"testing"

	"oc-go-cc/internal/config"
)

func TestNormalizeClaudeCategory(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		want      string
	}{
		{name: "haiku keyword", modelName: "claude-3-5-haiku-latest", want: "haiku"},
		{name: "sonnet keyword mixed case", modelName: "Claude-Sonnet-4", want: "sonnet"},
		{name: "opus keyword", modelName: "claude-opus-4-1", want: "opus"},
		{name: "unknown model", modelName: "deepseek-v4-pro", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeClaudeCategory(tt.modelName); got != tt.want {
				t.Fatalf("NormalizeClaudeCategory(%q) = %q, want %q", tt.modelName, got, tt.want)
			}
		})
	}
}

func TestLooksLikeDirectModelID(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		want      bool
	}{
		{name: "opencode model with dash", modelName: "deepseek-v4-pro", want: true},
		{name: "opencode model with dot", modelName: "qwen3.5-plus", want: true},
		{name: "claude category model is not direct", modelName: "claude-sonnet-4", want: false},
		{name: "plain scenario key is not direct", modelName: "default", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeDirectModelID(tt.modelName); got != tt.want {
				t.Fatalf("LooksLikeDirectModelID(%q) = %v, want %v", tt.modelName, got, tt.want)
			}
		})
	}
}

func TestRouteWithRequestModel_PrefersExactConfiguredModelName(t *testing.T) {
	router := NewModelRouter(&config.Config{
		ClaudeCode: config.ClaudeCodeConfig{
			Models: map[string]config.ModelConfig{
				"opus": {ModelID: "glm-5.1"},
			},
		},
		Models: map[string]config.ModelConfig{
			"deepseek-v4-pro": {Provider: "opencode-go", ModelID: "deepseek-v4-pro", MaxTokens: 8192},
			"default":          {ModelID: "qwen3.6-plus"},
		},
		Fallbacks: map[string][]config.ModelConfig{
			"deepseek-v4-pro": {{ModelID: "glm-5.1"}},
		},
	})

	result, err := router.RouteWithRequestModel("deepseek-v4-pro", []MessageContent{{Role: "user", Content: "hello"}}, 10)
	if err != nil {
		t.Fatalf("RouteWithRequestModel() error = %v", err)
	}

	if result.Primary.ModelID != "deepseek-v4-pro" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "deepseek-v4-pro")
	}
	if result.SelectionMode != SelectionModeRequestedModel {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeRequestedModel)
	}
	if result.RequestedModel != "deepseek-v4-pro" {
		t.Fatalf("RequestedModel = %q, want %q", result.RequestedModel, "deepseek-v4-pro")
	}
	if got := len(result.Fallbacks); got != 1 {
		t.Fatalf("len(Fallbacks) = %d, want 1", got)
	}
}

func TestRouteWithRequestModel_UsesDirectPassThroughForFullModelName(t *testing.T) {
	router := NewModelRouter(&config.Config{
		Models: map[string]config.ModelConfig{
			"default": {ModelID: "qwen3.6-plus"},
		},
	})

	result, err := router.RouteWithRequestModel("kimi-k2.5", []MessageContent{{Role: "user", Content: "hello"}}, 10)
	if err != nil {
		t.Fatalf("RouteWithRequestModel() error = %v", err)
	}

	if result.Primary.ModelID != "kimi-k2.5" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "kimi-k2.5")
	}
	if result.Primary.Provider != "opencode-go" {
		t.Fatalf("Primary.Provider = %q, want %q", result.Primary.Provider, "opencode-go")
	}
	if result.SelectionMode != SelectionModeRequestedModel {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeRequestedModel)
	}
	if got := len(result.Fallbacks); got != 0 {
		t.Fatalf("len(Fallbacks) = %d, want 0", got)
	}
}

func TestRouteWithRequestModel_PrefersClaudeCategoryMapping(t *testing.T) {
	router := NewModelRouter(&config.Config{
		ClaudeCode: config.ClaudeCodeConfig{
			Models: map[string]config.ModelConfig{
				"sonnet": {ModelID: "kimi-k2.6"},
			},
			Fallbacks: map[string][]config.ModelConfig{
				"sonnet": {{ModelID: "glm-5"}},
			},
		},
		Models: map[string]config.ModelConfig{
			"complex": {ModelID: "glm-5.1"},
			"default": {ModelID: "qwen3.6-plus"},
		},
	})

	messages := []MessageContent{{Role: "user", Content: "please architect a new system"}}

	result, err := router.RouteWithRequestModel("claude-sonnet-4", messages, 100)
	if err != nil {
		t.Fatalf("RouteWithRequestModel() error = %v", err)
	}

	if result.Primary.ModelID != "kimi-k2.6" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "kimi-k2.6")
	}
	if result.SelectionMode != SelectionModeClaudeCategory {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeClaudeCategory)
	}
	if result.RequestedCategory != "sonnet" {
		t.Fatalf("RequestedCategory = %q, want %q", result.RequestedCategory, "sonnet")
	}
	if got := len(result.Fallbacks); got != 1 {
		t.Fatalf("len(Fallbacks) = %d, want 1", got)
	}
}

func TestRouteWithRequestModel_FallsBackToScenarioRouting(t *testing.T) {
	router := NewModelRouter(&config.Config{
		Models: map[string]config.ModelConfig{
			"complex": {ModelID: "glm-5.1"},
			"default": {ModelID: "qwen3.6-plus"},
		},
	})

	messages := []MessageContent{{Role: "user", Content: "please architect a new system"}}

	result, err := router.RouteWithRequestModel("claude-sonnet-4", messages, 100)
	if err != nil {
		t.Fatalf("RouteWithRequestModel() error = %v", err)
	}

	if result.Primary.ModelID != "glm-5.1" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "glm-5.1")
	}
	if result.SelectionMode != SelectionModeScenario {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeScenario)
	}
	if result.Scenario != ScenarioComplex {
		t.Fatalf("Scenario = %q, want %q", result.Scenario, ScenarioComplex)
	}
}

func TestRouteForStreamingWithRequestModel_PrefersClaudeCategoryMapping(t *testing.T) {
	router := NewModelRouter(&config.Config{
		ClaudeCode: config.ClaudeCodeConfig{
			Models: map[string]config.ModelConfig{
				"opus": {ModelID: "deepseek-v4-pro"},
			},
		},
		Models: map[string]config.ModelConfig{
			"fast": {ModelID: "qwen3.6-plus"},
			"default": {ModelID: "kimi-k2.6"},
		},
	})

	messages := []MessageContent{{Role: "user", Content: "think step by step and architect the full system"}}

	result := router.RouteForStreamingWithRequestModel("claude-opus-4", messages, 100)

	if result.Primary.ModelID != "deepseek-v4-pro" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "deepseek-v4-pro")
	}
	if result.SelectionMode != SelectionModeClaudeCategory {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeClaudeCategory)
	}
	if result.RequestedCategory != "opus" {
		t.Fatalf("RequestedCategory = %q, want %q", result.RequestedCategory, "opus")
	}
}

func TestRouteForStreamingWithRequestModel_PrefersExactConfiguredModelName(t *testing.T) {
	router := NewModelRouter(&config.Config{
		Models: map[string]config.ModelConfig{
			"deepseek-v4-pro": {ModelID: "deepseek-v4-pro"},
			"fast":            {ModelID: "qwen3.6-plus"},
			"default":         {ModelID: "kimi-k2.6"},
		},
	})

	result := router.RouteForStreamingWithRequestModel("deepseek-v4-pro", []MessageContent{{Role: "user", Content: "think step by step"}}, 100)

	if result.Primary.ModelID != "deepseek-v4-pro" {
		t.Fatalf("Primary.ModelID = %q, want %q", result.Primary.ModelID, "deepseek-v4-pro")
	}
	if result.SelectionMode != SelectionModeRequestedModel {
		t.Fatalf("SelectionMode = %q, want %q", result.SelectionMode, SelectionModeRequestedModel)
	}
}