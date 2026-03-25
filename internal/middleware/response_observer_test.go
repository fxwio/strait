package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type observerRecorder struct {
	*httptest.ResponseRecorder
	flushed        int
	readFromCalled int
}

func newObserverRecorder() *observerRecorder {
	return &observerRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *observerRecorder) Flush() {
	r.flushed++
}

func (r *observerRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFromCalled++
	if r.Code == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return io.Copy(r.Body, src)
}

func TestResponseObserver_PreservesFlushAndReadFrom(t *testing.T) {
	rec := newObserverRecorder()
	observer := newResponseObserver(rec)

	if _, ok := any(observer).(http.Flusher); !ok {
		t.Fatal("expected response observer to implement http.Flusher")
	}
	if _, ok := any(observer).(io.ReaderFrom); !ok {
		t.Fatal("expected response observer to implement io.ReaderFrom")
	}

	n, err := observer.ReadFrom(strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != 5 {
		t.Fatalf("ReadFrom bytes = %d, want 5", n)
	}
	if rec.readFromCalled != 1 {
		t.Fatalf("underlying ReadFrom calls = %d, want 1", rec.readFromCalled)
	}
	if observer.bytes != 5 {
		t.Fatalf("observer bytes = %d, want 5", observer.bytes)
	}
	if observer.statusCode != http.StatusOK {
		t.Fatalf("observer status = %d, want 200", observer.statusCode)
	}

	observer.Flush()
	if rec.flushed != 1 {
		t.Fatalf("flush count = %d, want 1", rec.flushed)
	}
}

func TestResponseObserver_ReadFromFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	observer := newResponseObserver(rec)

	n, err := observer.ReadFrom(strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != int64(len("hello world")) {
		t.Fatalf("ReadFrom bytes = %d, want %d", n, len("hello world"))
	}
	if observer.bytes != len("hello world") {
		t.Fatalf("observer bytes = %d, want %d", observer.bytes, len("hello world"))
	}
	if observer.statusCode != http.StatusOK {
		t.Fatalf("observer status = %d, want 200", observer.statusCode)
	}
	if rec.Body.String() != "hello world" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "hello world")
	}
}
