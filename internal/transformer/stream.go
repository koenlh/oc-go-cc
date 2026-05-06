// Package transformer handles request/response transformation and token counting.
package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"oc-go-cc/internal/debuglog"
	"oc-go-cc/pkg/types"
)

// ErrClientDisconnected is returned when the client disconnects during streaming.
var ErrClientDisconnected = fmt.Errorf("client disconnected")

var errUpstreamStreamDone = fmt.Errorf("upstream stream done")

// StreamHandler handles streaming SSE transformation from OpenAI to Anthropic format.
type StreamHandler struct {
	responseTransformer *ResponseTransformer
	logPayloads         bool
}

type debugSSEWriter struct {
	http.ResponseWriter
	logPayloads bool
}

func (w *debugSSEWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *debugSSEWriter) sseDebugLoggingEnabled() bool {
	return w.logPayloads
}

type streamToolCallState struct {
	DeltaIndex   int
	ContentIndex int
	ToolID       string
	Name         string
	Started      bool
	Arguments    strings.Builder
}

type pendingMessageDelta struct {
	StopReason string
	Usage      *types.Usage
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler() *StreamHandler {
	return NewStreamHandlerWithPayloadLogging(false)
}

func NewStreamHandlerWithPayloadLogging(logPayloads bool) *StreamHandler {
	return &StreamHandler{
		responseTransformer: NewResponseTransformer(),
		logPayloads:         logPayloads,
	}
}

// ProxyStream takes an OpenAI streaming response and writes Anthropic-format SSE to the writer.
// It reads OpenAI ChatCompletionChunk SSE events and transforms them into Anthropic MessageEvent SSE events.
// The clientCtx is used to detect client disconnection and abort early.
//
// CRITICAL: This function reads directly from resp.Body without buffering to minimize latency.
// Per deep research: "Don't use bufio.Scanner or bufio.Reader on the response body - it adds buffering"
func (h *StreamHandler) ProxyStream(
	w http.ResponseWriter,
	openaiResp io.ReadCloser,
	originalModel string,
	clientCtx context.Context,
) error {
	if h.shouldLogPayloads() {
		w = &debugSSEWriter{ResponseWriter: w, logPayloads: true}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}

	// Generate a unique message ID for this stream.
	msgID := "msg_" + generateID()

	// Send message_start event with the full message envelope.
	msgStart := types.MessageEvent{
		Type: "message_start",
		Message: &types.MessageResponse{
			ID:      msgID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   originalModel,
		},
	}
	if err := writeSSEEvent(w, msgStart); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	// Read directly from response body without buffering.
	// Use a tight loop with a line buffer - no bufio.Reader.
	contentIndex := 0
	var lineBuf bytes.Buffer
	var reasoningText strings.Builder
	contentStarted := false
	reasoningStarted := false
	stopSent := false
	pendingFinalDelta := &pendingMessageDelta{}
	toolCallStates := make(map[int]*streamToolCallState)
	toolCallOrder := make([]int, 0)

	// Read in larger chunks for efficiency, then parse lines
	readBuf := make([]byte, 4096)
	startedAt := time.Now()
	loggedFirstRead := false

	streamDone := false
	for !streamDone {
		// Check if client disconnected
		select {
		case <-clientCtx.Done():
			return ErrClientDisconnected
		default:
		}

		// Read chunk from upstream
		n, err := openaiResp.Read(readBuf)
		if n > 0 {
			if !loggedFirstRead {
				loggedFirstRead = true
				slog.Debug("received first upstream stream bytes",
					"model", originalModel,
					"ttfb", time.Since(startedAt),
					"bytes", n,
				)
			}
			// Process bytes immediately
			for i := 0; i < n; i++ {
				b := readBuf[i]
				if b == '\n' {
					line := lineBuf.String()
					lineBuf.Reset()

					// Process complete line
					if err := h.processSSELine(w, flusher, line, &contentIndex, &contentStarted, &reasoningStarted, &reasoningText, &stopSent, pendingFinalDelta, toolCallStates, &toolCallOrder, originalModel); err != nil {
						if err == errUpstreamStreamDone {
							streamDone = true
							break
						}
						return err
					}
				} else {
					lineBuf.WriteByte(b)
				}
			}
		}

		if streamDone {
			break
		}

		if err == io.EOF {
			// Process any remaining data in buffer
			if lineBuf.Len() > 0 {
				line := lineBuf.String()
				if err := h.processSSELine(w, flusher, line, &contentIndex, &contentStarted, &reasoningStarted, &reasoningText, &stopSent, pendingFinalDelta, toolCallStates, &toolCallOrder, originalModel); err != nil {
					if err == errUpstreamStreamDone {
						break
					}
					return err
				}
			}
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read stream: %w", err)
		}
	}

	if reasoningStarted {
		if err := closeReasoningBlock(w, flusher, &contentIndex, &reasoningStarted, &reasoningText); err != nil {
			return err
		}
	}
	if contentStarted {
		if err := closeTextBlock(w, &contentIndex, &contentStarted, false); err != nil {
			return err
		}
	}
	if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, &toolCallOrder); err != nil {
		return err
	}
	if !stopSent {
		if err := emitPendingMessageDelta(w, flusher, pendingFinalDelta, "end_turn"); err != nil {
			return err
		}
		stopSent = true
	}

	// Send message_stop event to signal stream completion.
	stopEvent := types.MessageEvent{
		Type: "message_stop",
	}
	if err := writeSSEEvent(w, stopEvent); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	return nil
}

// processSSELine processes a single SSE line from upstream.
// Per deep research: "Treat SSE primarily as a text protocol" - minimize JSON parsing.
func (h *StreamHandler) processSSELine(
	w http.ResponseWriter,
	flusher http.Flusher,
	line string,
	contentIndex *int,
	contentStarted *bool,
	reasoningStarted *bool,
	reasoningText *strings.Builder,
	stopSent *bool,
	pendingFinalDelta *pendingMessageDelta,
	toolCallStates map[int]*streamToolCallState,
	toolCallOrder *[]int,
	originalModel string,
) error {
	line = strings.TrimSpace(line)
	shouldExposeThinking := isDeepSeekModel(originalModel)

	// Skip empty lines
	if line == "" {
		return nil
	}

	// Skip non-data lines (event: lines, id: lines, etc.)
	if !strings.HasPrefix(line, "data: ") {
		return nil
	}

	data := strings.TrimPrefix(line, "data: ")
	if data == "" {
		return nil
	}
	if h.shouldLogPayloads() {
		slog.Debug("received upstream stream chunk",
			"model", originalModel,
			"bytes", len(data),
			"preview", debuglog.PreviewString(data),
		)
	}

	// Handle [DONE] marker
	if data == "[DONE]" {
		if *reasoningStarted {
			if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
				return err
			}
		}
		if *contentStarted {
			if err := closeTextBlock(w, contentIndex, contentStarted, false); err != nil {
				return err
			}
		}
		if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, toolCallOrder); err != nil {
			return err
		}
		if !*stopSent {
			if err := emitPendingMessageDelta(w, flusher, pendingFinalDelta, "end_turn"); err != nil {
				return err
			}
			*stopSent = true
		}
		return errUpstreamStreamDone
	}

	// Fast path: only use direct text extraction for pure content chunks.
	// Mixed chunks (reasoning_content, tool_calls, finish_reason, usage) must
	// fall through to full JSON parsing so we don't silently drop fields.
	if canUseContentFastPath(data, shouldExposeThinking) {
		if content, ok := extractJSONStringFieldAfter(data, `"delta":{"content":"`); ok {
			if content != "" {
				if !*contentStarted {
					// If reasoning was already started, close it first
					if *reasoningStarted {
						if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
							return err
						}
					}
					*contentStarted = true
					// Send content_block_start
					startEvent := types.MessageEvent{
						Type:         "content_block_start",
						Index:        contentIndex,
						ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
					}
					if err := writeSSEEvent(w, startEvent); err != nil {
						return ErrClientDisconnected
					}
				}

				// Send content_block_delta
				delta := types.Delta{
					Type: "text_delta",
					Text: content,
				}
				event := types.MessageEvent{
					Type:  "content_block_delta",
					Index: contentIndex,
					Delta: &delta,
				}
				if err := writeSSEEvent(w, event); err != nil {
					return ErrClientDisconnected
				}
				flusher.Flush()
			}
			return nil
		}
	}

	// Check for pure finish_reason chunks. Mixed chunks (tool_calls/content/
	// reasoning/usage) must fall through to full JSON parsing so we don't drop
	// the final delta payload that accompanies the stop reason.
	if canUseFinishReasonFastPath(data, shouldExposeThinking) {
		finishReason := "end_turn"
		if rawFinishReason, ok := extractJSONStringFieldAfter(data, `"finish_reason":"`); ok {
			finishReason = h.responseTransformer.mapFinishReason(rawFinishReason)
		}

		// Close any open content block (reasoning or text)
		if *reasoningStarted {
			if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
				return err
			}
		}
		if *contentStarted {
			if err := closeTextBlock(w, contentIndex, contentStarted, false); err != nil {
				return err
			}
		}
		if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, toolCallOrder); err != nil {
			return err
		}

		// Send message_delta with stop_reason
		msgDelta := types.MessageEvent{
			Type: "message_delta",
			Delta: &types.Delta{
				StopReason: finishReason,
			},
		}
		_ = msgDelta
		pendingFinalDelta.StopReason = finishReason
		if pendingFinalDelta.Usage != nil {
			if err := emitPendingMessageDelta(w, flusher, pendingFinalDelta, finishReason); err != nil {
				return err
			}
			*stopSent = true
		}
		return nil
	}

	// For tool calls and other complex cases, fall back to full JSON parsing
	var chunk types.ChatCompletionChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		// Skip malformed chunks - don't fail the whole stream
		return nil
	}

	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil {
			if !*stopSent {
				pendingFinalDelta.Usage = usageInfoToAnthropic(chunk.Usage)
				if pendingFinalDelta.StopReason != "" {
					if err := emitPendingMessageDelta(w, flusher, pendingFinalDelta, pendingFinalDelta.StopReason); err != nil {
						return err
					}
					*stopSent = true
				}
			}
		}
		return nil
	}

	choice := chunk.Choices[0]
	if chunk.Usage != nil && !*stopSent {
		pendingFinalDelta.Usage = usageInfoToAnthropic(chunk.Usage)
	}

	// Handle reasoning content deltas
	if shouldExposeThinking && choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
		if !*reasoningStarted {
			// If text was already started, close it first
			if *contentStarted {
				if err := closeTextBlock(w, contentIndex, contentStarted, true); err != nil {
					return err
				}
			}
			if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, toolCallOrder); err != nil {
				return err
			}
			reasoningText.Reset()
			*reasoningStarted = true
			startEvent := types.MessageEvent{
				Type:         "content_block_start",
				Index:        contentIndex,
				ContentBlock: &types.ContentBlock{Type: "thinking", Thinking: ""},
			}
			if err := writeSSEEvent(w, startEvent); err != nil {
				return ErrClientDisconnected
			}
		}

		delta := types.Delta{
			Type:     "thinking_delta",
			Thinking: *choice.Delta.ReasoningContent,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: contentIndex,
			Delta: &delta,
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		reasoningText.WriteString(*choice.Delta.ReasoningContent)
		flusher.Flush()
	}

	// Handle text content deltas
	if choice.Delta.Content != "" {
		if !*contentStarted {
			// If reasoning was already started, close it first
			if *reasoningStarted {
				if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
					return err
				}
			}
			if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, toolCallOrder); err != nil {
				return err
			}
			*contentStarted = true
			startEvent := types.MessageEvent{
				Type:         "content_block_start",
				Index:        contentIndex,
				ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
			}
			if err := writeSSEEvent(w, startEvent); err != nil {
				return ErrClientDisconnected
			}
		}

		delta := types.Delta{
			Type: "text_delta",
			Text: choice.Delta.Content,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: contentIndex,
			Delta: &delta,
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
	}

	// Handle tool call deltas
	if len(choice.Delta.ToolCalls) > 0 {
		if *reasoningStarted {
			if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
				return err
			}
		}
		if *contentStarted {
			if err := closeTextBlock(w, contentIndex, contentStarted, true); err != nil {
				return err
			}
		}

		for i, tc := range choice.Delta.ToolCalls {
			deltaIndex := i
			if tc.Index != nil {
				deltaIndex = *tc.Index
			}

			state, ok := toolCallStates[deltaIndex]
			if !ok {
				state = &streamToolCallState{
					DeltaIndex:   deltaIndex,
					ContentIndex: *contentIndex,
					ToolID:       normalizeToolUseID(tc.ID, deltaIndex),
				}
				toolCallStates[deltaIndex] = state
				*toolCallOrder = append(*toolCallOrder, deltaIndex)
				*contentIndex++
			}

			if tc.ID != "" {
				state.ToolID = normalizeToolUseID(tc.ID, deltaIndex)
			}
			if tc.Function.Name != "" {
				state.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				state.Arguments.WriteString(tc.Function.Arguments)
			}

			if !state.Started && strings.TrimSpace(state.Name) != "" && state.Arguments.Len() > 0 {
				if err := startToolCallBlock(w, state); err != nil {
					return err
				}
			}
			if state.Started && state.Arguments.Len() > 0 {
				if err := flushToolCallArgumentsDelta(w, state); err != nil {
					return err
				}
			}
		}
		flusher.Flush()
	}

	// Handle finish reason
	if choice.FinishReason != "" {
		// Close any open content block (reasoning or text)
		if *reasoningStarted {
			if err := closeReasoningBlock(w, flusher, contentIndex, reasoningStarted, reasoningText); err != nil {
				return err
			}
		}
		if *contentStarted {
			if err := closeTextBlock(w, contentIndex, contentStarted, false); err != nil {
				return err
			}
		}

		if err := closeOpenToolCallBlocks(w, flusher, toolCallStates, toolCallOrder); err != nil {
			return err
		}

		pendingFinalDelta.StopReason = h.responseTransformer.mapFinishReason(choice.FinishReason)
		if pendingFinalDelta.Usage != nil {
			if err := emitPendingMessageDelta(w, flusher, pendingFinalDelta, pendingFinalDelta.StopReason); err != nil {
				return err
			}
			*stopSent = true
		}
	}

	return nil
}

func emitPendingMessageDelta(
	w http.ResponseWriter,
	flusher http.Flusher,
	pending *pendingMessageDelta,
	defaultStopReason string,
) error {
	stopReason := defaultStopReason
	if pending != nil && pending.StopReason != "" {
		stopReason = pending.StopReason
	}
	event := types.MessageEvent{
		Type: "message_delta",
		Delta: &types.Delta{
			StopReason: stopReason,
		},
	}
	if pending != nil {
		event.Usage = pending.Usage
	}
	if err := writeSSEEvent(w, event); err != nil {
		return ErrClientDisconnected
	}
	if pending != nil {
		pending.StopReason = ""
		pending.Usage = nil
	}
	flusher.Flush()
	return nil
}

func (h *StreamHandler) sendUsageDelta(w http.ResponseWriter, flusher http.Flusher, usage *types.UsageInfo) error {
	event := types.MessageEvent{
		Type: "message_delta",
		Delta: &types.Delta{
			StopReason: "end_turn",
		},
		Usage: usageInfoToAnthropic(usage),
	}
	if err := writeSSEEvent(w, event); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()
	return nil
}

func closeReasoningBlock(
	w http.ResponseWriter,
	flusher http.Flusher,
	contentIndex *int,
	reasoningStarted *bool,
	reasoningText *strings.Builder,
) error {
	if !*reasoningStarted {
		return nil
	}

	signature := syntheticThinkingSignature(reasoningText.String())
	if signature != "" {
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: contentIndex,
			Delta: &types.Delta{
				Type:      "signature_delta",
				Signature: signature,
			},
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
	}

	stopEvent := types.MessageEvent{
		Type:  "content_block_stop",
		Index: contentIndex,
	}
	if err := writeSSEEvent(w, stopEvent); err != nil {
		return ErrClientDisconnected
	}

	*contentIndex++
	*reasoningStarted = false
	reasoningText.Reset()
	return nil
}

func closeTextBlock(
	w http.ResponseWriter,
	contentIndex *int,
	contentStarted *bool,
	advanceIndex bool,
) error {
	if !*contentStarted {
		return nil
	}

	stopEvent := types.MessageEvent{
		Type:  "content_block_stop",
		Index: contentIndex,
	}
	if err := writeSSEEvent(w, stopEvent); err != nil {
		return ErrClientDisconnected
	}

	*contentStarted = false
	if advanceIndex {
		*contentIndex++
	}
	return nil
}

func startToolCallBlock(w http.ResponseWriter, state *streamToolCallState) error {
	if state.Started {
		return nil
	}

	startEvent := types.MessageEvent{
		Type:  "content_block_start",
		Index: &state.ContentIndex,
		ContentBlock: &types.ContentBlock{
			Type:  "tool_use",
			ID:    state.ToolID,
			Name:  normalizeToolUseName(state.Name),
			Input: json.RawMessage(`{}`),
		},
	}
	if err := writeSSEEvent(w, startEvent); err != nil {
		return ErrClientDisconnected
	}

	state.Started = true
	return nil
}

func flushToolCallArgumentsDelta(w http.ResponseWriter, state *streamToolCallState) error {
	if state.Arguments.Len() == 0 {
		return nil
	}

	delta := types.Delta{
		Type:        "input_json_delta",
		PartialJSON: state.Arguments.String(),
	}
	event := types.MessageEvent{
		Type:  "content_block_delta",
		Index: &state.ContentIndex,
		Delta: &delta,
	}
	if err := writeSSEEvent(w, event); err != nil {
		return ErrClientDisconnected
	}

	state.Arguments.Reset()
	return nil
}

func closeOpenToolCallBlocks(
	w http.ResponseWriter,
	flusher http.Flusher,
	toolCallStates map[int]*streamToolCallState,
	toolCallOrder *[]int,
) error {
	if len(*toolCallOrder) == 0 {
		return nil
	}

	for _, deltaIndex := range *toolCallOrder {
		state := toolCallStates[deltaIndex]
		if state == nil {
			continue
		}
		if !state.Started {
			if err := startToolCallBlock(w, state); err != nil {
				return err
			}
		}
		if state.Arguments.Len() > 0 {
			if err := flushToolCallArgumentsDelta(w, state); err != nil {
				return err
			}
		}

		stopEvent := types.MessageEvent{
			Type:  "content_block_stop",
			Index: &state.ContentIndex,
		}
		if err := writeSSEEvent(w, stopEvent); err != nil {
			return ErrClientDisconnected
		}
		delete(toolCallStates, deltaIndex)
	}

	*toolCallOrder = (*toolCallOrder)[:0]
	flusher.Flush()
	return nil
}

func usageInfoToAnthropic(usage *types.UsageInfo) *types.Usage {
	if usage == nil {
		return nil
	}
	return &types.Usage{
		InputTokens:              usage.PromptTokens,
		OutputTokens:             usage.CompletionTokens,
		CacheCreationInputTokens: usage.PromptCacheMissTokens,
		CacheReadInputTokens:     usage.PromptCacheHitTokens,
	}
}

// writeSSEEvent writes a single SSE event to the HTTP response writer.
// Format: "event: <type>\ndata: <json>\n\n"
func writeSSEEvent(w http.ResponseWriter, event types.MessageEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	if shouldLogSSEEvent(w) {
		logSSEEvent(event, data)
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
	return err
}

type sseDebugLogger interface {
	sseDebugLoggingEnabled() bool
}

func shouldLogSSEEvent(w http.ResponseWriter) bool {
	logger, ok := w.(sseDebugLogger)
	return ok && logger.sseDebugLoggingEnabled() && slog.Default().Enabled(context.Background(), slog.LevelDebug)
}

func logSSEEvent(event types.MessageEvent, data []byte) {
	fields := []any{
		"event", event.Type,
		"bytes", len(data),
		"preview", debuglog.PreviewBytes(data),
	}
	if event.Index != nil {
		fields = append(fields, "index", *event.Index)
	}
	if event.ContentBlock != nil {
		fields = append(fields,
			"content_block_type", event.ContentBlock.Type,
			"content_block_id", event.ContentBlock.ID,
			"content_block_name", event.ContentBlock.Name,
		)
	}
	if event.Delta != nil {
		fields = append(fields,
			"delta_type", event.Delta.Type,
			"stop_reason", event.Delta.StopReason,
		)
	}
	if event.Usage != nil {
		fields = append(fields,
			"input_tokens", event.Usage.InputTokens,
			"output_tokens", event.Usage.OutputTokens,
		)
	}
	slog.Debug("sending anthropic stream event", fields...)
}

func (h *StreamHandler) shouldLogPayloads() bool {
	return h != nil && h.logPayloads && slog.Default().Enabled(context.Background(), slog.LevelDebug)
}

// generateID creates a unique identifier based on current time.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func canUseContentFastPath(data string, shouldExposeThinking bool) bool {
	if strings.Contains(data, `"tool_calls"`) || strings.Contains(data, `"finish_reason":`) || strings.Contains(data, `"usage":`) {
		return false
	}
	if shouldExposeThinking && strings.Contains(data, `"reasoning_content"`) {
		return false
	}
	return true
}

func canUseFinishReasonFastPath(data string, shouldExposeThinking bool) bool {
	if !strings.Contains(data, `"finish_reason":`) || strings.Contains(data, `"finish_reason":null`) {
		return false
	}
	if strings.Contains(data, `"usage":`) || strings.Contains(data, `"tool_calls"`) || strings.Contains(data, `"content":`) {
		return false
	}
	if shouldExposeThinking && strings.Contains(data, `"reasoning_content"`) {
		return false
	}
	return true
}

func extractJSONStringFieldAfter(data string, marker string) (string, bool) {
	idx := strings.Index(data, marker)
	if idx == -1 {
		return "", false
	}

	start := idx + len(marker)
	var escaped bool
	for i := start; i < len(data); i++ {
		switch data[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if escaped {
				escaped = false
				continue
			}
			decoded, err := strconv.Unquote(`"` + data[start:i] + `"`)
			if err != nil {
				return "", false
			}
			return decoded, true
		default:
			escaped = false
		}
	}

	return "", false
}
