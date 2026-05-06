package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"oc-go-cc/pkg/types"
)

// mockResponseWriter implements http.ResponseWriter and http.Flusher for testing.
type mockResponseWriter struct {
	buf    bytes.Buffer
	header http.Header
	status int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		header: make(http.Header),
	}
}

func (m *mockResponseWriter) Header() http.Header         { return m.header }
func (m *mockResponseWriter) Write(p []byte) (int, error) { return m.buf.Write(p) }
func (m *mockResponseWriter) WriteHeader(statusCode int)  { m.status = statusCode }
func (m *mockResponseWriter) Flush()                      {}

// sseLines builds raw SSE body from a list of data payloads.
func sseLines(lines ...string) io.ReadCloser {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

// parseSSEEvents parses the raw response buffer into a slice of MessageEvent.
func parseSSEEvents(t *testing.T, raw string) []types.MessageEvent {
	t.Helper()
	var events []types.MessageEvent
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev types.MessageEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("unmarshal SSE event: %v (data: %s)", err, data)
			}
			events = append(events, ev)
		}
	}
	return events
}

func parseSSEDataPayloads(raw string) []string {
	var payloads []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data != "" && data != "[DONE]" {
				payloads = append(payloads, data)
			}
		}
	}
	return payloads
}

func TestProxyStream_ReasoningContentFastPath(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Let me think"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":" step by step"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start, 2x thinking_delta, signature_delta,
	// content_block_stop, message_delta, message_stop
	if len(events) != 8 {
		t.Fatalf("expected 8 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != "message_start" {
		t.Errorf("event[0].Type = %q, want message_start", events[0].Type)
	}
	if events[1].Type != "content_block_start" {
		t.Errorf("event[1].Type = %q, want content_block_start", events[1].Type)
	}
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1].ContentBlock = %+v, want thinking block", events[1].ContentBlock)
	}
	if events[2].Type != "content_block_delta" {
		t.Errorf("event[2].Type = %q, want content_block_delta", events[2].Type)
	}
	if got := events[2].Delta.Type; got != "thinking_delta" {
		t.Errorf("event[2].Delta.Type = %q, want thinking_delta", got)
	}
	if got := events[2].Delta.Thinking; got != "Let me think" {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", got, "Let me think")
	}
	if events[3].Type != "content_block_delta" {
		t.Errorf("event[3].Type = %q, want content_block_delta", events[3].Type)
	}
	if got := events[3].Delta.Thinking; got != " step by step" {
		t.Errorf("event[3].Delta.Thinking = %q, want %q", got, " step by step")
	}
	if got := events[4].Delta.Type; got != "signature_delta" {
		t.Errorf("event[4].Delta.Type = %q, want signature_delta", got)
	}
	if got, want := events[4].Delta.Signature, syntheticThinkingSignature("Let me think step by step"); got != want {
		t.Errorf("event[4].Delta.Signature = %q, want %q", got, want)
	}
	if events[5].Type != "content_block_stop" {
		t.Errorf("event[5].Type = %q, want content_block_stop", events[5].Type)
	}
	if events[6].Type != "message_delta" {
		t.Errorf("event[6].Type = %q, want message_delta", events[6].Type)
	}
	if events[7].Type != "message_stop" {
		t.Errorf("event[7].Type = %q, want message_stop", events[7].Type)
	}
}

func TestProxyStream_ReasoningThenText(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}`,
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start(thinking, idx=0), thinking_delta,
	//           signature_delta, content_block_stop(idx=0), content_block_start(text, idx=1),
	//           text_delta x2, content_block_stop(idx=1), message_delta, message_stop
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Verify indexes
	if got := *events[1].Index; got != 0 {
		t.Errorf("thinking start index = %d, want 0", got)
	}
	if got := *events[4].Index; got != 0 {
		t.Errorf("thinking stop index = %d, want 0", got)
	}
	if got := *events[5].Index; got != 1 {
		t.Errorf("text start index = %d, want 1", got)
	}
	if got := *events[8].Index; got != 1 {
		t.Errorf("text stop index = %d, want 1", got)
	}

	// Verify types
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1].ContentBlock = %+v, want thinking block", events[1].ContentBlock)
	}
	if got := events[2].Delta.Type; got != "thinking_delta" {
		t.Errorf("event[2].Delta.Type = %q, want thinking_delta", got)
	}
	if got := events[3].Delta.Type; got != "signature_delta" {
		t.Errorf("event[3].Delta.Type = %q, want signature_delta", got)
	}
	if got, want := events[3].Delta.Signature, syntheticThinkingSignature("Thinking..."); got != want {
		t.Errorf("event[3].Delta.Signature = %q, want %q", got, want)
	}
	if events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5].ContentBlock = %+v, want text block", events[5].ContentBlock)
	}
	if got := events[6].Delta.Type; got != "text_delta" {
		t.Errorf("event[6].Delta.Type = %q, want text_delta", got)
	}
}

func TestProxyStream_TextOnlyStillWorks(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start, 2x content_block_delta, content_block_stop, message_delta, message_stop
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "text_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if events[2].Delta.Text != "Hello" {
		t.Errorf("event[2].Delta.Text = %q, want Hello", events[2].Delta.Text)
	}

	payloads := parseSSEDataPayloads(w.buf.String())
	if !strings.Contains(payloads[1], `"content_block":{"type":"text","text":""}`) {
		t.Fatalf("text content_block_start payload = %s, want explicit empty text", payloads[1])
	}
	if strings.Contains(payloads[5], `"type":""`) {
		t.Fatalf("message_delta payload = %s, want no empty delta.type", payloads[5])
	}
	if !strings.Contains(payloads[5], `"stop_sequence":null`) {
		t.Fatalf("message_delta payload = %s, want explicit null stop_sequence", payloads[5])
	}
}

func TestProxyStream_TextWithEscapedQuotesFastPath(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"{\"title\": \"Fix text box effect lag on fast typing\"}"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Type != "text_delta" {
		t.Fatalf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if got, want := events[2].Delta.Text, `{"title": "Fix text box effect lag on fast typing"}`; got != want {
		t.Fatalf("event[2].Delta.Text = %q, want %q", got, want)
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Fatalf("event[4] = %+v, want message_delta(end_turn)", events[4])
	}
	if events[5].Type != "message_stop" {
		t.Fatalf("event[5].Type = %q, want message_stop", events[5].Type)
	}
}

func TestProxyStream_DoneSendsMessageStopAndIgnoresTrailingChunks(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`[DONE]`,
		`{"choices":[],"cost":"0"}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Text != "Hello" {
		t.Fatalf("event[2] = %+v, want text_delta Hello", events[2])
	}
	if events[3].Type != "content_block_stop" {
		t.Fatalf("event[3] = %+v, want content_block_stop", events[3])
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Fatalf("event[4] = %+v, want message_delta(end_turn)", events[4])
	}
	if events[5].Type != "message_stop" {
		t.Fatalf("event[5] = %+v, want message_stop", events[5])
	}
}

func TestProxyStream_UsageOnlyChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":123,"completion_tokens":45,"total_tokens":168,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":23}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var usage *types.Usage
	for _, event := range events {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage event found in stream: %+v", events)
	}
	if got, want := usage.InputTokens, 123; got != want {
		t.Fatalf("InputTokens = %d, want %d", got, want)
	}
	if got, want := usage.OutputTokens, 45; got != want {
		t.Fatalf("OutputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheReadInputTokens, 100; got != want {
		t.Fatalf("CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheCreationInputTokens, 23; got != want {
		t.Fatalf("CacheCreationInputTokens = %d, want %d", got, want)
	}
}

// TestProxyStream_NoDuplicateMessageDelta verifies that when finish_reason and
// usage arrive in separate chunks, they are coalesced into a single final
// message_delta event.
func TestProxyStream_NoDuplicateMessageDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	var messageDeltas []types.MessageEvent
	for _, ev := range events {
		if ev.Type == "message_delta" {
			messageDeltas = append(messageDeltas, ev)
		}
	}

	if len(messageDeltas) != 1 {
		t.Fatalf("expected exactly 1 message_delta, got %d: %+v", len(messageDeltas), messageDeltas)
	}
	if got, want := messageDeltas[0].Delta.StopReason, "end_turn"; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if messageDeltas[0].Usage == nil {
		t.Fatalf("message_delta usage = nil, want usage included: %+v", messageDeltas[0])
	}

	if got, want := messageDeltas[0].Usage.InputTokens, 100; got != want {
		t.Errorf("InputTokens = %d, want %d", got, want)
	}
}

func TestProxyStream_ToolUseFinishAndStandaloneUsageBecomeSingleFinalDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_pwd","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"pwd\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`,
		`[DONE]`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var messageDeltas []types.MessageEvent
	for _, ev := range events {
		if ev.Type == "message_delta" {
			messageDeltas = append(messageDeltas, ev)
		}
	}

	if len(messageDeltas) != 1 {
		t.Fatalf("expected exactly 1 message_delta, got %d: %+v", len(messageDeltas), messageDeltas)
	}
	if got, want := messageDeltas[0].Delta.StopReason, "tool_use"; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if messageDeltas[0].Usage == nil {
		t.Fatalf("message_delta usage = nil, want usage included: %+v", messageDeltas[0])
	}
	if got, want := messageDeltas[0].Usage.OutputTokens, 3; got != want {
		t.Fatalf("OutputTokens = %d, want %d", got, want)
	}
}

func TestProxyStream_ReasoningJSONFallback(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	// This payload does NOT match the fast-path string pattern because of extra whitespace,
	// forcing the JSON fallback path.
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{ReasoningContent: strPtr("Reasoning via JSON")})),
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start, thinking_delta, signature_delta,
	// content_block_stop, message_delta, message_stop
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Reasoning via JSON" {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Reasoning via JSON")
	}
	if got := events[3].Delta.Type; got != "signature_delta" {
		t.Errorf("event[3].Delta.Type = %q, want signature_delta", got)
	}
	if got, want := events[3].Delta.Signature, syntheticThinkingSignature("Reasoning via JSON"); got != want {
		t.Errorf("event[3].Delta.Signature = %q, want %q", got, want)
	}
}

func TestProxyStream_EmptyReasoningContentSkipped(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{ReasoningContent: strPtr("")})),
		`{"choices":[{"delta":{"content":"Only text"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Empty reasoning should be skipped; only one text chunk -> 6 events total
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if *events[1].Index != 0 {
		t.Errorf("text start index = %d, want 0", *events[1].Index)
	}
}

func TestProxyStream_ReasoningAndContentInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{
			ReasoningContent: strPtr("Thinking..."),
			Content:          "Hello",
		})),
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// message_start + thinking_start + thinking_delta + signature_delta + thinking_stop +
	// text_start + text_delta("Hello") + text_delta(" world") + text_stop +
	// message_delta + message_stop = 11
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Block 0: thinking
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Thinking..." {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Thinking...")
	}
	if events[3].Type != "content_block_delta" || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want content_block_delta(signature_delta)", events[3])
	}
	if got, want := events[3].Delta.Signature, syntheticThinkingSignature("Thinking..."); got != want {
		t.Errorf("event[3].Delta.Signature = %q, want %q", got, want)
	}
	if events[4].Type != "content_block_stop" {
		t.Errorf("event[4].Type = %q, want content_block_stop", events[4].Type)
	}

	// Block 1: text
	if events[5].Type != "content_block_start" || events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5] = %+v, want content_block_start(text)", events[5])
	}
	if events[6].Type != "content_block_delta" || events[6].Delta.Type != "text_delta" {
		t.Errorf("event[6] = %+v, want content_block_delta(text_delta)", events[6])
	}
	if events[6].Delta.Text != "Hello" {
		t.Errorf("event[6].Delta.Text = %q, want Hello", events[6].Delta.Text)
	}
	if events[7].Type != "content_block_delta" || events[7].Delta.Type != "text_delta" {
		t.Errorf("event[7] = %+v, want content_block_delta(text_delta)", events[7])
	}
	if events[7].Delta.Text != " world" {
		t.Errorf("event[7].Delta.Text = %q, want \" world\"", events[7].Delta.Text)
	}
	if events[8].Type != "content_block_stop" {
		t.Errorf("event[8].Type = %q, want content_block_stop", events[8].Type)
	}
}

// TestProxyStream_ReasoningBeforeContentFastPathRegression ensures that when
// a provider sends reasoning_content BEFORE content in the same delta (with no
// role field), the fast path for content is skipped and reasoning_content is
// not silently dropped. If it were dropped, the next turn would fail on
// DeepSeek with "reasoning_content must be passed back".
func TestProxyStream_ReasoningBeforeContentFastPathRegression(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	// Hand-crafted JSON: reasoning_content appears before content, no role field.
	// Before the fix, the fast path matched "delta":{"content":" and returned
	// early, discarding reasoning_content entirely.
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking...","content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// message_start + thinking_start + thinking_delta + signature_delta + thinking_stop +
	// text_start + text_delta("Hello") + text_delta(" world") + text_stop +
	// message_delta + message_stop = 11
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Block 0: thinking (must NOT be lost)
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Thinking..." {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Thinking...")
	}
	if events[3].Type != "content_block_delta" || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want content_block_delta(signature_delta)", events[3])
	}
	if got, want := events[3].Delta.Signature, syntheticThinkingSignature("Thinking..."); got != want {
		t.Errorf("event[3].Delta.Signature = %q, want %q", got, want)
	}

	// Block 1: text
	if events[5].Type != "content_block_start" || events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5] = %+v, want content_block_start(text)", events[5])
	}
	if events[6].Delta.Text != "Hello" {
		t.Errorf("event[6].Delta.Text = %q, want Hello", events[6].Delta.Text)
	}
}

func TestProxyStream_AccumulatesToolCallDeltasIntoSingleToolUseBlock(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"path\":\"README"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":".md\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil {
		t.Fatalf("event[1] = %+v, want content_block_start(tool_use)", events[1])
	}
	if got, want := events[1].ContentBlock.Type, "tool_use"; got != want {
		t.Fatalf("event[1].ContentBlock.Type = %q, want %q", got, want)
	}
	if got, want := events[1].ContentBlock.Name, "read_file"; got != want {
		t.Fatalf("event[1].ContentBlock.Name = %q, want %q", got, want)
	}
	if got, want := events[1].ContentBlock.ID, "toolu_call_123"; got != want {
		t.Fatalf("event[1].ContentBlock.ID = %q, want %q", got, want)
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Type != "input_json_delta" {
		t.Fatalf("event[2] = %+v, want first input_json_delta", events[2])
	}
	if got, want := events[2].Delta.PartialJSON, `{"path":"README`; got != want {
		t.Fatalf("event[2].Delta.PartialJSON = %q, want %q", got, want)
	}
	if events[3].Type != "content_block_delta" || events[3].Delta == nil || events[3].Delta.Type != "input_json_delta" {
		t.Fatalf("event[3] = %+v, want second input_json_delta", events[3])
	}
	if got, want := events[3].Delta.PartialJSON, `.md"}`; got != want {
		t.Fatalf("event[3].Delta.PartialJSON = %q, want %q", got, want)
	}
	if events[4].Type != "content_block_stop" {
		t.Fatalf("event[4].Type = %q, want content_block_stop", events[4].Type)
	}
	if events[5].Type != "message_delta" || events[5].Delta == nil || events[5].Delta.StopReason != "tool_use" {
		t.Fatalf("event[5] = %+v, want message_delta(tool_use)", events[5])
	}
	if events[6].Type != "message_stop" {
		t.Fatalf("event[6].Type = %q, want message_stop", events[6].Type)
	}
}

func TestProxyStream_ContentAndToolCallInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Let me explore the codebase first.\n\n","tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 9 {
		t.Fatalf("expected 9 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Type != "text_delta" {
		t.Fatalf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if got, want := events[2].Delta.Text, "Let me explore the codebase first.\n\n"; got != want {
		t.Fatalf("event[2].Delta.Text = %q, want %q", got, want)
	}
	if events[3].Type != "content_block_stop" {
		t.Fatalf("event[3].Type = %q, want content_block_stop", events[3].Type)
	}
	if events[4].Type != "content_block_start" || events[4].ContentBlock == nil || events[4].ContentBlock.Type != "tool_use" {
		t.Fatalf("event[4] = %+v, want content_block_start(tool_use)", events[4])
	}
	if got, want := events[4].ContentBlock.Name, "list_dir"; got != want {
		t.Fatalf("event[4].ContentBlock.Name = %q, want %q", got, want)
	}
	if got, want := events[4].ContentBlock.ID, "toolu_call_123"; got != want {
		t.Fatalf("event[4].ContentBlock.ID = %q, want %q", got, want)
	}
	if events[5].Type != "content_block_delta" || events[5].Delta == nil || events[5].Delta.Type != "input_json_delta" {
		t.Fatalf("event[5] = %+v, want content_block_delta(input_json_delta)", events[5])
	}
	if got, want := events[5].Delta.PartialJSON, `{}`; got != want {
		t.Fatalf("event[5].Delta.PartialJSON = %q, want %q", got, want)
	}
	if events[6].Type != "content_block_stop" {
		t.Fatalf("event[6] = %+v, want content_block_stop", events[6])
	}
	if events[7].Type != "message_delta" || events[7].Delta == nil || events[7].Delta.StopReason != "tool_use" {
		t.Fatalf("event[7] = %+v, want message_delta(tool_use)", events[7])
	}
	if events[8].Type != "message_stop" {
		t.Fatalf("event[8] = %+v, want message_stop", events[8])
	}
}

func TestProxyStream_ToolCallAndFinishReasonInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Let me inspect the files first.\n\n"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"glob","arguments":"{\"pattern\":\"**/*.{ts,tsx,js,jsx}\"}"}}]},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 9 {
		t.Fatalf("expected 9 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Type != "text_delta" {
		t.Fatalf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if events[3].Type != "content_block_stop" {
		t.Fatalf("event[3] = %+v, want content_block_stop", events[3])
	}
	if events[4].Type != "content_block_start" || events[4].ContentBlock == nil || events[4].ContentBlock.Type != "tool_use" {
		t.Fatalf("event[4] = %+v, want content_block_start(tool_use)", events[4])
	}
	if got, want := events[4].ContentBlock.Name, "glob"; got != want {
		t.Fatalf("event[4].ContentBlock.Name = %q, want %q", got, want)
	}
	if got, want := events[4].ContentBlock.ID, "toolu_call_123"; got != want {
		t.Fatalf("event[4].ContentBlock.ID = %q, want %q", got, want)
	}
	if events[5].Type != "content_block_delta" || events[5].Delta == nil || events[5].Delta.Type != "input_json_delta" {
		t.Fatalf("event[5] = %+v, want content_block_delta(input_json_delta)", events[5])
	}
	if got, want := events[5].Delta.PartialJSON, `{"pattern":"**/*.{ts,tsx,js,jsx}"}`; got != want {
		t.Fatalf("event[5].Delta.PartialJSON = %q, want %q", got, want)
	}
	if events[6].Type != "content_block_stop" {
		t.Fatalf("event[6] = %+v, want content_block_stop", events[6])
	}
	if events[7].Type != "message_delta" || events[7].Delta == nil || events[7].Delta.StopReason != "tool_use" {
		t.Fatalf("event[7] = %+v, want message_delta(tool_use)", events[7])
	}
	if events[8].Type != "message_stop" {
		t.Fatalf("event[8] = %+v, want message_stop", events[8])
	}
}

func TestProxyStream_OmitsReasoningBlocksForNonDeepSeekModels(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Internal reasoning","content":"{\"title\":\"Fix text box effect lag on fast typing\"}"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Type != "text_delta" {
		t.Fatalf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if got, want := events[2].Delta.Text, `{"title":"Fix text box effect lag on fast typing"}`; got != want {
		t.Fatalf("event[2].Delta.Text = %q, want %q", got, want)
	}
	if events[3].Type != "content_block_stop" {
		t.Fatalf("event[3].Type = %q, want content_block_stop", events[3].Type)
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Fatalf("event[4] = %+v, want message_delta(end_turn)", events[4])
	}
	if events[5].Type != "message_stop" {
		t.Fatalf("event[5].Type = %q, want message_stop", events[5].Type)
	}
}

// helpers

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func strPtr(s string) *string { return &s }
