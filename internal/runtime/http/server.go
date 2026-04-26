package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"
)

// Server wraps a net/http.Server and drives its lifecycle. Constructed
// by cmd/vtuber-worker-node after the Mux has been fully populated
// (unpaid handlers + every capability module). Start() blocks until
// Shutdown() is called or the server fails.
type Server struct {
	mux    *Mux
	inner  *nethttp.Server
	logger *slog.Logger
}

// NewServer wires a Server against a populated Mux. The listen address
// comes from Config.Worker.HTTPListen; not taken here because the mux
// already has cfg threaded through.
func NewServer(mux *Mux, listen string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		mux: mux,
		inner: &nethttp.Server{
			Addr:              listen,
			Handler:           mux.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// Start binds the listener and begins serving. Blocks until the
// server stops (clean Shutdown returns nil; unexpected errors bubble).
func (s *Server) Start() error {
	s.logger.Info("vtuber-worker-node serving", "addr", s.inner.Addr)
	err := s.inner.ListenAndServe()
	if errors.Is(err, nethttp.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("http server: %w", err)
}

// Shutdown triggers a graceful stop. In-flight requests drain up to
// ctx's deadline; remaining connections are forcibly closed.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.inner.Shutdown(ctx)
}
