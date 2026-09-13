// Package http wires the HTTP surface: public capture endpoint, JSON API, health probes
// and the embedded single page application.
//
// The order of the middlewares matters:
//
//	logreq -> webhook capture -> mux -> {/api/v1, /healthz, SPA}
//
// Placing the capture middleware before the mux means a request to /hooks/<token> is
// answered without ever reaching the router, and the 1 MiB body limit is applied before
// any handler can buffer it.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/version"
	appHttpMiddleware "github.com/yuandzhang/webhook-zq/internal/http/middleware/logreq"
	"github.com/yuandzhang/webhook-zq/internal/http/middleware/secure"
	"github.com/yuandzhang/webhook-zq/internal/http/middleware/webhook"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// Server wraps http.Server with a graceful shutdown timeout.
type Server struct {
	server *http.Server

	// ShutdownTimeout bounds the graceful stop.
	ShutdownTimeout time.Duration
}

// Option customises the underlying http.Server.
type Option func(*Server)

// WithReadTimeout sets the read timeout.
func WithReadTimeout(d time.Duration) Option { return func(s *Server) { s.server.ReadTimeout = d } }

// WithWriteTimeout sets the write timeout.
func WithWriteTimeout(d time.Duration) Option { return func(s *Server) { s.server.WriteTimeout = d } }

// WithIdleTimeout sets the keep-alive timeout.
func WithIdleTimeout(d time.Duration) Option { return func(s *Server) { s.server.IdleTimeout = d } }

// NewServer creates a server bound to the provided base context.
func NewServer(baseCtx context.Context, log *zap.Logger, opts ...Option) *Server {
	s := Server{
		server: &http.Server{ //nolint:gosec // read header timeout is set explicitly by the caller
			BaseContext: func(net.Listener) context.Context { return baseCtx },
			ErrorLog:    zap.NewStdLog(log),
		},
		ShutdownTimeout: 5 * time.Second,
	}

	for _, opt := range opts {
		opt(&s)
	}

	return &s
}

// Deps are everything the HTTP layer needs.
type Deps struct {
	Log      *zap.Logger
	Settings *config.AppSettings
	Store    storage.Store
	PubSub   pubsub.PubSub[notify.Message]
	Cipher   *crypto.Cipher // nil when no encryption key is configured
	API      http.Handler   // mounted at /api
}

// Register builds the handler chain.
func (s *Server) Register(appCtx context.Context, d Deps, spa http.Handler) *Server {
	mux := http.NewServeMux()

	mux.Handle("/api/", http.StripPrefix("/api", d.API))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Store.Ping(r.Context()); err != nil {
			d.Log.Warn("healthz failed", zap.Error(err))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "version": version.Version()})

			return
		}

		// The version is on the probe as well as on /api/v1/settings: when two machines
		// behave differently, `curl /healthz` is the first thing anyone runs.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": version.Version()})
	})

	mux.Handle("/", spa)

	var handler http.Handler = mux
	handler = webhook.New(appCtx, webhook.Deps{
		Log:        d.Log.Named("webhook"),
		Store:      d.Store,
		Pub:        d.PubSub,
		Settings:   d.Settings,
		Cipher:     d.Cipher,
		TrustProxy: d.Settings != nil && d.Settings.TrustProxy,
	})(handler)
	handler = secure.New()(handler)
	handler = appHttpMiddleware.New(d.Log, func(r *http.Request) bool {
		return r.URL.Path == "/healthz"
	})(handler)

	s.server.Handler = handler

	return s
}

// StartHTTP serves on the listener until the context is cancelled.
func (s *Server) StartHTTP(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error)

	go func(ch chan<- error) {
		defer close(ch)

		ch <- s.server.Serve(ln)
	}(errCh)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.ShutdownTimeout)
		defer cancel()

		if err := s.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case err, ok := <-errCh:
		switch {
		case !ok:
			return nil
		case err != nil:
			return err
		}
	}

	return nil
}
