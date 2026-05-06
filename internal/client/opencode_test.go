package client

import (
	"testing"

	"oc-go-cc/pkg/types"
)

func TestIsAnthropicModelOnlyRoutesNativeAnthropicModels(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		{
			name:    "minimax m2.5 uses anthropic endpoint",
			modelID: "minimax-m2.5",
			want:    true,
		},
		{
			name:    "minimax m2.7 uses anthropic endpoint",
			modelID: "minimax-m2.7",
			want:    true,
		},
		{
			name:    "deepseek pro uses openai endpoint",
			modelID: "deepseek-v4-pro",
			want:    false,
		},
		{
			name:    "deepseek flash uses openai endpoint",
			modelID: "deepseek-v4-flash",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAnthropicModel(tt.modelID); got != tt.want {
				t.Fatalf("IsAnthropicModel(%q) = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestSanitizeChatCompletionRequestForEndpoint_StripsCacheControlForOpenAIEndpoint(t *testing.T) {
	input := &types.ChatCompletionRequest{
		Model: "glm-5.1",
		Messages: []types.ChatMessage{
			{
				Role:         "system",
				Content:      "You are helpful",
				CacheControl: &types.CacheControl{Type: "ephemeral"},
			},
			{Role: "user", Content: "hello"},
		},
	}

	sanitized := sanitizeChatCompletionRequestForEndpoint(input, "glm-5.1")
	if sanitized == input {
		t.Fatal("sanitizeChatCompletionRequestForEndpoint() returned original request, want cloned request")
	}
	if sanitized.Messages[0].CacheControl != nil {
		t.Fatalf("sanitized.Messages[0].CacheControl = %v, want nil", sanitized.Messages[0].CacheControl)
	}
	if input.Messages[0].CacheControl == nil {
		t.Fatal("input.Messages[0].CacheControl = nil, want original request unchanged")
	}
}

func TestSanitizeChatCompletionRequestForEndpoint_PreservesCacheControlForAnthropicEndpoint(t *testing.T) {
	input := &types.ChatCompletionRequest{
		Model: "minimax-m2.7",
		Messages: []types.ChatMessage{{
			Role:         "system",
			Content:      "You are helpful",
			CacheControl: &types.CacheControl{Type: "ephemeral"},
		}},
	}

	sanitized := sanitizeChatCompletionRequestForEndpoint(input, "minimax-m2.7")
	if sanitized != input {
		t.Fatal("sanitizeChatCompletionRequestForEndpoint() returned cloned request, want original for Anthropic endpoint")
	}
	if sanitized.Messages[0].CacheControl == nil {
		t.Fatal("sanitized.Messages[0].CacheControl = nil, want preserved cache_control")
	}
}
