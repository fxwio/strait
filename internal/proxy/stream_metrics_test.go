package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// flushRecorder extends httptest.ResponseRecorder with Flush support so
// ttftWriter can exercise the flush path in tests.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (r *flushRecorder) Flush() {
	r.flushed++
	r.ResponseRecorder.Flush()
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

// TestTTFTWriter_RecordsTTFTOnFirstWrite verifies that the first Write records
// a non-zero TTFT and that subsequent writes do not update it.
func TestTTFTWriter_RecordsTTFTOnFirstWrite(t *testing.T) {
	w := newTTFTWriter(newFlushRecorder(), "openai", "gpt-4o", time.Now().Add(-50*time.Millisecond))

	if w.firstChunkSeen {
		t.Fatal("expected firstChunkSeen=false before any write")
	}

	_, _ = w.Write([]byte("chunk1"))
	if !w.firstChunkSeen {
		t.Fatal("expected firstChunkSeen=true after first write")
	}
	ttft1 := w.ttft

	if ttft1 <= 0 {
		t.Fatalf("expected positive TTFT, got %v", ttft1)
	}

	// Second write must not overwrite ttft.
	time.Sleep(5 * time.Millisecond)
	_, _ = w.Write([]byte("chunk2"))
	if w.ttft != ttft1 {
		t.Fatalf("TTFT changed after second write: was %v, now %v", ttft1, w.ttft)
	}
}

// TestTTFTWriter_FlushesOnEveryWrite verifies that each Write triggers a Flush
// on the underlying ResponseWriter.
func TestTTFTWriter_FlushesOnEveryWrite(t *testing.T) {
	rec := newFlushRecorder()
	w := newTTFTWriter(rec, "openai", "gpt-4o", time.Now())

	for i := 0; i < 3; i++ {
		_, _ = w.Write([]byte("data: {}\n\n"))
	}

	if rec.flushed != 3 {
		t.Fatalf("expected 3 flushes, got %d", rec.flushed)
	}
}

// TestTTFTWriter_BodyPassedThrough verifies that written bytes reach the
// underlying ResponseWriter.
func TestTTFTWriter_BodyPassedThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTTFTWriter(rec, "openai", "gpt-4o", time.Now())

	payload := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
	_, err := w.Write([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("expected body to contain 'hello', got: %q", rec.Body.String())
	}
}

// TestTTFTWriter_RecordMetrics_NoWriteCalled verifies that RecordMetrics does
// not panic and records StreamDuration even when no Write was called (empty stream).
func TestTTFTWriter_RecordMetrics_NoWriteCalled(t *testing.T) {
	w := newTTFTWriter(httptest.NewRecorder(), "openai", "gpt-4o-mini", time.Now().Add(-100*time.Millisecond))

	// Must not panic even when firstChunkSeen == false.
	w.RecordMetrics("empty")

	if w.firstChunkSeen {
		t.Fatal("firstChunkSeen should remain false when no Write was called")
	}
}

// TestTTFTWriter_RecordMetrics_WithWrite verifies that RecordMetrics works
// normally after at least one Write (the typical streaming path).
func TestTTFTWriter_RecordMetrics_WithWrite(t *testing.T) {
	start := time.Now().Add(-200 * time.Millisecond)
	w := newTTFTWriter(httptest.NewRecorder(), "anthropic", "claude-3-5-sonnet-20241022", start)

	_, _ = w.Write([]byte("data: {}\n\n"))

	// RecordMetrics must not panic and TTFT must be <= total elapsed.
	w.RecordMetrics("completed")

	total := time.Since(start)
	if w.ttft > total {
		t.Fatalf("TTFT (%v) should be <= total elapsed (%v)", w.ttft, total)
	}
}

func TestClassifyStreamOutcome(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if got := classifyStreamOutcome(req, nil, true); got != "completed" {
		t.Fatalf("completed outcome = %q, want completed", got)
	}
	if got := classifyStreamOutcome(req, nil, false); got != "empty" {
		t.Fatalf("empty outcome = %q, want empty", got)
	}

	canceledReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(canceledReq.Context())
	cancel()
	canceledReq = canceledReq.WithContext(ctx)
	if got := classifyStreamOutcome(canceledReq, context.Canceled, true); got != "client_canceled" {
		t.Fatalf("client cancel outcome = %q, want client_canceled", got)
	}

	if got := classifyStreamOutcome(req, io.ErrUnexpectedEOF, true); got != "upstream_error" {
		t.Fatalf("upstream error outcome = %q, want upstream_error", got)
	}
}

// TestTTFTWriter_ImplementsResponseWriter confirms the type satisfies the
// http.ResponseWriter interface so it can be passed anywhere that accepts one.
func TestTTFTWriter_ImplementsResponseWriter(t *testing.T) {
	var _ http.ResponseWriter = newTTFTWriter(httptest.NewRecorder(), "", "", time.Now())
}

// TestTTFTWriter_HeadersForwarded verifies that Header() delegates to the
// underlying writer (important for setting Content-Type before WriteHeader).
func TestTTFTWriter_HeadersForwarded(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTTFTWriter(rec, "openai", "gpt-4o", time.Now())

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type to be forwarded, got: %q", rec.Header().Get("Content-Type"))
	}
}

// TestTTFTWriter_MultipleChunkOrder checks that TTFT is strictly less than the
// time of the last write, confirming it captures the first and not the last chunk.
func TestTTFTWriter_MultipleChunkOrder(t *testing.T) {
	buf := &bytes.Buffer{}
	rec := &struct {
		http.ResponseWriter
	}{httptest.NewRecorder()}
	_ = rec

	start := time.Now()
	w := newTTFTWriter(httptest.NewRecorder(), "openai", "gpt-4o", start)

	_, _ = w.Write([]byte("first"))
	time.Sleep(10 * time.Millisecond)
	_, _ = w.Write([]byte("second"))

	_ = buf

	// TTFT should be less than the time elapsed by the second write.
	secondWriteElapsed := time.Since(start)
	if w.ttft >= secondWriteElapsed {
		t.Fatalf("TTFT (%v) should be < second write elapsed (%v)", w.ttft, secondWriteElapsed)
	}
}
