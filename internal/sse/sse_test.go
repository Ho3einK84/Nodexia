package sse_test

import (
	"net/http/httptest"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/sse"
)

func TestWriterEventSplitsMultilineData(t *testing.T) {
	rec := httptest.NewRecorder()
	w := sse.NewWriter(rec)

	if err := w.Event("output", "line1\nline2\n"); err != nil {
		t.Fatalf("Event: %v", err)
	}

	// Each line of the chunk becomes its own `data:` line; the browser rejoins
	// them with "\n". A trailing newline yields a final empty data line.
	want := "event: output\ndata: line1\ndata: line2\ndata: \n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("event frame = %q, want %q", got, want)
	}
}

func TestWriterEventNormalisesCRLF(t *testing.T) {
	rec := httptest.NewRecorder()
	w := sse.NewWriter(rec)

	if err := w.Event("output", "a\r\nb"); err != nil {
		t.Fatalf("Event: %v", err)
	}

	want := "event: output\ndata: a\ndata: b\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("event frame = %q, want %q", got, want)
	}
}

func TestWriterHeadersAndComment(t *testing.T) {
	rec := httptest.NewRecorder()
	w := sse.NewWriter(rec)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if ab := rec.Header().Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", ab)
	}

	if err := w.Comment("keep-alive"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if got := rec.Body.String(); got != ": keep-alive\n\n" {
		t.Fatalf("comment frame = %q, want %q", got, ": keep-alive\n\n")
	}
}
