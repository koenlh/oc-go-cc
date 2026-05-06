package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oc-go-cc/internal/client"
	"oc-go-cc/internal/config"
)

type countingResponseWriter struct {
	header           http.Header
	writeHeaderCount atomic.Int32
	writeCount       atomic.Int32
}

func newCountingResponseWriter() *countingResponseWriter {
	return &countingResponseWriter{header: make(http.Header)}
}

func (w *countingResponseWriter) Header() http.Header {
	return w.header
}

func (w *countingResponseWriter) WriteHeader(statusCode int) {
	w.writeHeaderCount.Add(1)
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	w.writeCount.Add(1)
	return len(p), nil
}

func (w *countingResponseWriter) Flush() {}

func TestExecuteAnthropicRequestRewritesModelForNonStreaming(t *testing.T) {
	var requestBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"minimax-m2.7","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	openCodeClient := client.NewOpenCodeClient(config.OpenCodeGoConfig{
		BaseURL:          "https://example.invalid/v1/chat/completions",
		AnthropicBaseURL: server.URL,
		TimeoutMs:        1000,
	}, "test-key", config.LoggingConfig{})

	handler := &MessagesHandler{
		client: openCodeClient,
		logger: slog.Default(),
	}

	_, err := handler.executeAnthropicRequest(
		context.Background(),
		[]byte(`{"model":"claude-sonnet-4","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`),
		config.ModelConfig{ModelID: "minimax-m2.7"},
	)
	if err != nil {
		t.Fatalf("executeAnthropicRequest() error = %v", err)
	}

	if !strings.Contains(requestBody, `"model":"minimax-m2.7"`) {
		t.Fatalf("request body = %s, want rewritten minimax model", requestBody)
	}
	if strings.Contains(requestBody, `"model":"claude-sonnet-4"`) {
		t.Fatalf("request body = %s, want original Claude model to be replaced", requestBody)
	}
}

func TestResponseWriterDoesNotDoubleWriteHeader(t *testing.T) {
	base := newCountingResponseWriter()
	rw := &responseWriter{ResponseWriter: base}

	rw.WriteHeader(http.StatusOK)
	if _, err := rw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	rw.Flush()

	if got := base.writeHeaderCount.Load(); got != 1 {
		t.Fatalf("WriteHeader count = %d, want 1", got)
	}
	if got := base.writeCount.Load(); got != 1 {
		t.Fatalf("Write count = %d, want 1", got)
	}
}

func TestStreamAttemptTimeoutUsesConfigTimeout(t *testing.T) {
	handler := &MessagesHandler{
		config: &config.Config{
			OpenCodeGo: config.OpenCodeGoConfig{TimeoutMs: 420000},
		},
	}

	if got, want := handler.streamAttemptTimeout(), 420*time.Second; got != want {
		t.Fatalf("streamAttemptTimeout() = %v, want %v", got, want)
	}
}

func TestStreamAttemptTimeoutFallsBackToDefault(t *testing.T) {
	handler := &MessagesHandler{
		config: &config.Config{},
	}

	if got, want := handler.streamAttemptTimeout(), defaultStreamAttemptTimeout; got != want {
		t.Fatalf("streamAttemptTimeout() = %v, want %v", got, want)
	}
}