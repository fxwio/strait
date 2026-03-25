package router

import (
	"net/http"
	"sync/atomic"

	"github.com/fxwio/strait/internal/response"
)

var draining atomic.Bool

func SetDraining(v bool) {
	draining.Store(v)
}

func IsDraining() bool {
	return draining.Load()
}

func drainingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsDraining() {
			w.Header().Set("Connection", "close")
			response.WriteServiceUnavailable(w, "Server is shutting down.", "server_shutting_down")
			return
		}

		next.ServeHTTP(w, r)
	})
}
