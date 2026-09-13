// Package secure sets the baseline response headers every web facing application should
// send. It is deliberately small: three static headers that cannot break a page.
//
// A Content-Security-Policy is intentionally NOT set here. Mantine injects inline styles,
// so a strict CSP silently breaks the UI; tightening it is a deliberate follow up that
// needs its own testing pass.
package secure

import "net/http"

// New wraps a handler and adds the security headers.
func New() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()

			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")

			next.ServeHTTP(w, r)
		})
	}
}
