// Package logreq logs every incoming request with enough structure to trace one webhook
// from capture to replay in a single grep.
package logreq

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const requestIDHeader = "X-Request-Id"

// New creates a middleware that logs every incoming request.
//
// It also makes sure every request has an id: an incoming X-Request-Id is honoured (so a
// proxy chain stays correlatable), otherwise one is generated and written back to the
// response. Without it, "who sent this webhook" is unanswerable from the logs alone.
//
// The skipper function should return true if the request should be skipped. It's ok to
// pass nil.
func New(log *zap.Logger, skipper func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipper != nil && skipper(r) {
				next.ServeHTTP(w, r)

				return
			}

			var now = time.Now()

			id := r.Header.Get(requestIDHeader)
			if id == "" {
				id = newRequestID()
				r.Header.Set(requestIDHeader, id)
			}

			w.Header().Set(requestIDHeader, id)

			defer func() {
				// Keys use snake_case and are unique: duplicated keys make structured
				// logs hard to query (and silently drop data in some collectors).
				fields := []zap.Field{
					zap.String("request_id", id),
					zap.String("method", r.Method),
					zap.String("url", r.URL.String()),
					zap.String("host", r.Host),
					zap.String("user_agent", r.UserAgent()),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("content_type", w.Header().Get("Content-Type")),
					zap.Duration("duration", time.Since(now).Round(time.Microsecond)),
				}

				if log.Level() <= zap.DebugLevel {
					fields = append(fields,
						zap.Any("request_headers", r.Header.Clone()),
						zap.Any("response_headers", w.Header().Clone()),
					)
				}

				log.Info("HTTP request processed", fields...)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func newRequestID() string {
	var b [16]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}

	return hex.EncodeToString(b[:])
}
