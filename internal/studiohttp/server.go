// Package studiohttp configures the local Studio HTTP services.
package studiohttp

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// New creates a server whose request contexts follow the application's lifetime.
// Shutdown closes unfinished browser preconnections without interrupting active
// requests. Otherwise net/http waits for their first headers for up to five
// seconds, which can exhaust Studio's entire graceful shutdown deadline.
func New(ctx context.Context, handler http.Handler) *http.Server {
	var mu sync.Mutex
	pending := make(map[net.Conn]struct{})
	stopping := false
	s := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	s.ConnState = func(conn net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		if state == http.StateNew {
			if stopping {
				_ = conn.Close()
			} else {
				pending[conn] = struct{}{}
			}
		} else {
			delete(pending, conn)
		}
	}
	s.RegisterOnShutdown(func() {
		mu.Lock()
		defer mu.Unlock()
		stopping = true
		for conn := range pending {
			_ = conn.Close()
			delete(pending, conn)
		}
	})
	return s
}
