package debuglog

import (
	"strings"
	"testing"
)

func TestPreviewBytesPreservesShortBodies(t *testing.T) {
	body := []byte(`{"temperature":0.7}`)
	if got := PreviewBytes(body); got != string(body) {
		t.Fatalf("PreviewBytes() = %q, want %q", got, string(body))
	}
}

func TestPreviewStringTruncatesLargeBodies(t *testing.T) {
	body := strings.Repeat("a", DefaultPreviewLimit+128)
	got := PreviewString(body)
	if !strings.HasPrefix(got, strings.Repeat("a", DefaultPreviewLimit)) {
		t.Fatal("PreviewString() did not preserve the leading content")
	}
	if !strings.Contains(got, "(truncated 128 bytes)") {
		t.Fatalf("PreviewString() = %q, want truncation suffix", got)
	}
}
