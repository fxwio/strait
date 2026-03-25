package proxy

import (
	"net/http"
	"time"

	gatewaymetrics "github.com/fxwio/strait/internal/metrics"
)

// ttftWriter wraps http.ResponseWriter to instrument streaming responses with
// two Prometheus histograms:
//
//   - gateway_stream_ttft_seconds — Time-to-First-Token: elapsed time from
//     attemptStart until the first Write call (i.e., when the first SSE chunk
//     is forwarded to the client).
//   - gateway_stream_duration_seconds — total wall-clock time from attemptStart
//     until RecordMetrics is called (i.e., the full streaming session).
//
// It also flushes after every Write so each SSE chunk reaches the client
// immediately without buffering (replaces the separate flushWriter for the
// streaming path).
type ttftWriter struct {
	http.ResponseWriter

	start    time.Time
	provider string
	model    string

	firstChunkSeen bool
	ttft           time.Duration
	bytesWritten   int64
	chunkCount     int
}

func newTTFTWriter(w http.ResponseWriter, provider, model string, start time.Time) *ttftWriter {
	return &ttftWriter{
		ResponseWriter: w,
		start:          start,
		provider:       provider,
		model:          model,
	}
}

// Write records TTFT on the first invocation, then writes p to the underlying
// ResponseWriter and immediately flushes so the SSE chunk reaches the client.
func (w *ttftWriter) Write(p []byte) (int, error) {
	if !w.firstChunkSeen {
		w.firstChunkSeen = true
		w.ttft = time.Since(w.start)
	}
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.bytesWritten += int64(n)
		w.chunkCount++
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

// RecordMetrics emits the TTFT and total duration observations to Prometheus.
// Call this exactly once after the stream is fully drained (regardless of error).
// If no Write was ever called (e.g., upstream returned an empty stream), only
// StreamDuration is recorded.
func (w *ttftWriter) RecordMetrics(outcome string) {
	elapsed := time.Since(w.start)
	if w.firstChunkSeen {
		gatewaymetrics.StreamTTFT.
			WithLabelValues(w.provider, w.model).
			Observe(w.ttft.Seconds())
	}
	gatewaymetrics.StreamDuration.
		WithLabelValues(w.provider, w.model).
		Observe(elapsed.Seconds())
	gatewaymetrics.StreamTerminationsTotal.
		WithLabelValues(w.provider, w.model, outcome).
		Inc()
}
