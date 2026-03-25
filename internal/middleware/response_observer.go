package middleware

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

const responseObserverCopyBufferSize = 32 << 10

var responseObserverCopyBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, responseObserverCopyBufferSize)
	},
}

type responseObserver struct {
	http.ResponseWriter
	statusCode int
	bytes      int
}

func newResponseObserver(w http.ResponseWriter) *responseObserver {
	return &responseObserver{ResponseWriter: w}
}

func (w *responseObserver) WriteHeader(code int) {
	if w.statusCode == 0 {
		w.statusCode = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseObserver) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *responseObserver) ReadFrom(r io.Reader) (int64, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}

	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		w.bytes += int(n)
		return n, err
	}

	buf := responseObserverCopyBufferPool.Get().([]byte)
	n, err := io.CopyBuffer(w.ResponseWriter, r, buf)
	responseObserverCopyBufferPool.Put(buf)
	w.bytes += int(n)
	return n, err
}

func (w *responseObserver) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *responseObserver) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
