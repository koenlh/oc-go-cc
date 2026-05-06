package transformer

import (
	"encoding/json"
	"testing"

	"oc-go-cc/pkg/types"
)

func TestTransformResponsePreservesReasoningContent(t *testing.T) {
	transformer := NewResponseTransformer()

	reasoning := "Let me think about this step by step"
	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "deepseek-v4-flash",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:             "assistant",
					Content:          "The answer is 42.",
					ReasoningContent: &reasoning,
				},
				FinishReason: "stop",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := len(anthropicResp.Content), 2; got != want {
		t.Fatalf("len(Content) = %d, want %d", got, want)
	}

	if got, want := anthropicResp.Content[0].Type, "thinking"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[0].Thinking, reasoning; got != want {
		t.Fatalf("Content[0].Thinking = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[0].Signature, syntheticThinkingSignature(reasoning); got != want {
		t.Fatalf("Content[0].Signature = %q, want %q", got, want)
	}

	if got, want := anthropicResp.Content[1].Type, "text"; got != want {
		t.Fatalf("Content[1].Type = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[1].Text, "The answer is 42."; got != want {
		t.Fatalf("Content[1].Text = %q, want %q", got, want)
	}
}

func TestTransformResponsePreservesReasoningContentWithToolCalls(t *testing.T) {
	transformer := NewResponseTransformer()

	reasoning := "I need to call a tool to get the weather"
	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_456",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "deepseek-v4-pro",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:             "assistant",
					Content:          "",
					ReasoningContent: &reasoning,
					ToolCalls: []types.ToolCall{
						{
							ID:   "call_123",
							Type: "function",
							Function: types.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"city":"Kigali"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     20,
			CompletionTokens: 15,
			TotalTokens:      35,
		},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := len(anthropicResp.Content), 2; got != want {
		t.Fatalf("len(Content) = %d, want %d", got, want)
	}

	if got, want := anthropicResp.Content[0].Type, "thinking"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[0].Thinking, reasoning; got != want {
		t.Fatalf("Content[0].Thinking = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[0].Signature, syntheticThinkingSignature(reasoning); got != want {
		t.Fatalf("Content[0].Signature = %q, want %q", got, want)
	}

	if got, want := anthropicResp.Content[1].Type, "tool_use"; got != want {
		t.Fatalf("Content[1].Type = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[1].ID, "toolu_call_123"; got != want {
		t.Fatalf("Content[1].ID = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[1].Name, "get_weather"; got != want {
		t.Fatalf("Content[1].Name = %q, want %q", got, want)
	}

	if got, want := anthropicResp.StopReason, "tool_use"; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
}

func TestTransformResponseSanitizesInvalidToolArguments(t *testing.T) {
	transformer := NewResponseTransformer()

	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_bad_args",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "deepseek-v4-pro",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:    "assistant",
					Content: "",
					ToolCalls: []types.ToolCall{
						{
							ID:   "call_bad",
							Type: "function",
							Function: types.FunctionCall{
								Name:      "search_docs",
								Arguments: `{"query":"unterminated"`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     12,
			CompletionTokens: 7,
			TotalTokens:      19,
		},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := anthropicResp.Content[0].Type, "tool_use"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
	if got, want := string(anthropicResp.Content[0].Input), `{}`; got != want {
		t.Fatalf("Content[0].Input = %s, want %s", got, want)
	}

	if _, err := json.Marshal(anthropicResp); err != nil {
		t.Fatalf("json.Marshal(anthropicResp) error = %v", err)
	}
}

func TestTransformResponseSynthesizesMissingToolUseFields(t *testing.T) {
	transformer := NewResponseTransformer()

	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_missing_tool_fields",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "qwen3.6-plus",
		Choices: []types.Choice{{
			Index: 0,
			Message: types.ChatMessage{
				Role:    "assistant",
				Content: "",
				ToolCalls: []types.ToolCall{{
					Function: types.FunctionCall{Arguments: `{"path":"README.md"}`},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: types.UsageInfo{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "qwen3.6-plus")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	block := anthropicResp.Content[0]
	if got, want := block.Type, "tool_use"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
	if got, want := block.ID, "toolu_generated_0"; got != want {
		t.Fatalf("Content[0].ID = %q, want %q", got, want)
	}
	if got, want := block.Name, "unknown_tool"; got != want {
		t.Fatalf("Content[0].Name = %q, want %q", got, want)
	}
}

func TestTransformResponseOmitsReasoningContentForNonDeepSeekModels(t *testing.T) {
	transformer := NewResponseTransformer()

	reasoning := "Internal reasoning that should not be surfaced"
	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_qwen_reasoning",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "qwen3.6-plus",
		Choices: []types.Choice{{
			Index: 0,
			Message: types.ChatMessage{
				Role:             "assistant",
				Content:          `{"title":"Fix text box effect lag on fast typing"}`,
				ReasoningContent: &reasoning,
			},
			FinishReason: "stop",
		}},
		Usage: types.UsageInfo{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "qwen3.6-plus")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := len(anthropicResp.Content), 1; got != want {
		t.Fatalf("len(Content) = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Content[0].Type, "text"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
	if got, want := anthropicResp.Content[0].Text, `{"title":"Fix text box effect lag on fast typing"}`; got != want {
		t.Fatalf("Content[0].Text = %q, want %q", got, want)
	}
}

func TestTransformResponseOmitsEmptyReasoningContent(t *testing.T) {
	transformer := NewResponseTransformer()

	emptyReasoning := ""
	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_789",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "kimi-k2.6",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:             "assistant",
					Content:          "Hello there.",
					ReasoningContent: &emptyReasoning,
				},
				FinishReason: "stop",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     5,
			CompletionTokens: 2,
			TotalTokens:      7,
		},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "kimi-k2.6")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := len(anthropicResp.Content), 1; got != want {
		t.Fatalf("len(Content) = %d, want %d", got, want)
	}

	if got, want := anthropicResp.Content[0].Type, "text"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
}

func TestTransformResponseNoReasoningContent(t *testing.T) {
	transformer := NewResponseTransformer()

	resp := &types.ChatCompletionResponse{
		ID:      "chatcmpl_abc",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "kimi-k2.6",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:    "assistant",
					Content: "Just a plain response.",
				},
				FinishReason: "stop",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     3,
			CompletionTokens: 4,
			TotalTokens:      7,
		},
	}

	anthropicResp, err := transformer.TransformResponse(resp, "kimi-k2.6")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := len(anthropicResp.Content), 1; got != want {
		t.Fatalf("len(Content) = %d, want %d", got, want)
	}

	if got, want := anthropicResp.Content[0].Type, "text"; got != want {
		t.Fatalf("Content[0].Type = %q, want %q", got, want)
	}
}

func TestTransformResponseWithCacheTokens(t *testing.T) {
	transformer := NewResponseTransformer()

	openaiResp := &types.ChatCompletionResponse{
		ID:     "chatcmpl-123",
		Object: "chat.completion",
		Model:  "kimi-k2.6",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:    "assistant",
					Content: "Hello, world!",
				},
				FinishReason: "stop",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:          100,
			CompletionTokens:      50,
			TotalTokens:           150,
			PromptCacheHitTokens:  80,
			PromptCacheMissTokens: 20,
		},
	}

	anthropicResp, err := transformer.TransformResponse(openaiResp, "claude-3-sonnet")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := anthropicResp.Usage.InputTokens, 100; got != want {
		t.Errorf("Usage.InputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.OutputTokens, 50; got != want {
		t.Errorf("Usage.OutputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.CacheReadInputTokens, 80; got != want {
		t.Errorf("Usage.CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.CacheCreationInputTokens, 20; got != want {
		t.Errorf("Usage.CacheCreationInputTokens = %d, want %d", got, want)
	}
}

func TestTransformResponseWithoutCacheTokens(t *testing.T) {
	transformer := NewResponseTransformer()

	openaiResp := &types.ChatCompletionResponse{
		ID:     "chatcmpl-456",
		Object: "chat.completion",
		Model:  "glm-5",
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.ChatMessage{
					Role:    "assistant",
					Content: "No cache here",
				},
				FinishReason: "stop",
			},
		},
		Usage: types.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	anthropicResp, err := transformer.TransformResponse(openaiResp, "claude-3-haiku")
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	if got, want := anthropicResp.Usage.InputTokens, 10; got != want {
		t.Errorf("Usage.InputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.OutputTokens, 5; got != want {
		t.Errorf("Usage.OutputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.CacheReadInputTokens, 0; got != want {
		t.Errorf("Usage.CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := anthropicResp.Usage.CacheCreationInputTokens, 0; got != want {
		t.Errorf("Usage.CacheCreationInputTokens = %d, want %d", got, want)
	}
}
