// Package webhook implements the public receive endpoint: POST /hooks/{token}.
//
// It is a middleware (not a route) on purpose: it inspects the path first and either
// handles the request completely or passes it down to the API/SPA. That keeps the hot
// path free of routing overhead and guarantees that a request to /hooks/... can never be
// served by the single page application.
//
// Behaviour, in order:
//
//	no token / unknown token -> 404 (do not leak which tokens exist)
//	inbox disabled           -> 403
//	body over the limit      -> 413, nothing is stored
//	signature required+wrong -> 401, but the event is still stored (marked invalid) so the
//	                            user can debug *why* it was rejected
//	otherwise                -> 200 + {"id": "<event id>"} (or the inbox's custom response)
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// hooksPrefix is the public path segment every receive URL starts with.
const hooksPrefix = "hooks/"

// Deps are the middleware dependencies.
type Deps struct {
	Log      *zap.Logger
	Store    storage.Store
	Pub      pubsub.Publisher[notify.Message]
	Settings *config.AppSettings
	Cipher   *crypto.Cipher

	// TrustProxy enables reading the client IP from X-Forwarded-For & friends. It is a
	// deployment decision (only safe behind a proxy that rewrites those headers) and must
	// never be derived from the request itself.
	TrustProxy bool
}

// New builds the capture middleware.
func New(appCtx context.Context, d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := extractToken(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)

				return
			}

			handleCapture(appCtx, d, w, r, token)
		})
	}
}

// extractToken returns the token from /hooks/<token>[/extra/path].
func extractToken(path string) (string, bool) {
	clean := strings.TrimLeft(path, "/")
	if !strings.HasPrefix(clean, hooksPrefix) {
		return "", false
	}

	rest := clean[len(hooksPrefix):]

	if rest == "" {
		return "", false
	}

	token := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		token = rest[:i]
	}

	// We deliberately do NOT validate the token alphabet here: anything under /hooks/ is
	// a capture attempt, and an unknown or malformed token must be answered with 404 by
	// the capture handler. Validating here would hand the request to the SPA and answer
	// 200 with an HTML page - confusing for users and noisy for scanners.
	if token == "" || len(token) > 64 {
		return "", false
	}

	return token, true
}

func handleCapture(
	appCtx context.Context,
	d Deps,
	w http.ResponseWriter,
	r *http.Request,
	token string,
) {
	// CORS is set before any branch, not only on the success path: this endpoint is meant
	// to be called from a browser on another origin, and an error response without these
	// headers is reported by the browser as a CORS failure - which hides the real reason
	// (unknown token, disabled inbox, body too large) from the caller entirely.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	// A preflight is not a webhook: answer it before anything is looked up or stored,
	// otherwise every browser-side demo would pollute the event list. It has to come
	// before the enabled/disabled check too - a refused preflight is indistinguishable
	// from a network error in the browser.
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	inbox, err := d.Store.GetInboxByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, storage.ErrInboxNotFound) {
			writeError(w, http.StatusNotFound, "unknown_token", "unknown webhook token")

			return
		}

		d.Log.Error("cannot resolve inbox by token", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")

		return
	}

	if !inbox.Enabled {
		writeError(w, http.StatusForbidden, "inbox_disabled", "this inbox is disabled")

		return
	}

	// Cheap rejection for requests that declare a body larger than the limit: we still
	// need the streaming check below (Content-Length can be absent with chunked encoding),
	// but this avoids reading anything at all in the obvious case.
	if r.ContentLength > int64(d.Settings.MaxRequestBodySize) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf(
			"request body is too large (limit is %d bytes)", d.Settings.MaxRequestBodySize))

		return
	}

	// Size limit: http.MaxBytesReader stops reading as soon as the budget is exceeded and
	// signals the client, so an oversized body is never buffered in full.
	limit := int64(d.Settings.MaxRequestBodySize)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit+1))
	if err != nil {
		var tooLarge *http.MaxBytesError

		if errors.As(err, &tooLarge) {
			// Nothing is persisted: a rejected request must not consume retention budget.
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf(
				"request body is too large (limit is %d bytes)", d.Settings.MaxRequestBodySize))

			return
		}

		d.Log.Warn("cannot read request body", zap.Error(err))
		writeError(w, http.StatusBadRequest, "bad_request", "cannot read request body")

		return
	}

	if int64(len(body)) > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf(
			"request body is too large (limit is %d bytes)", d.Settings.MaxRequestBodySize))

		return
	}

	// Signature verification runs on the raw bytes, before anything is stored.
	//
	// Fail closed: if "require signature" is on but no secret is configured (or the
	// secret could not be decrypted), the request is rejected. Treating "not verified" as
	// "verified" would let anyone post to an inbox that the UI advertises as protected.
	signatureValid := verifySignature(inbox, body, r.Header.Get(inbox.SignatureHeader))
	if inbox.RequireSignature && (signatureValid == nil || !*signatureValid) {
		storeIncoming(appCtx, d, inbox, r, body, signatureValid)

		writeError(w, http.StatusUnauthorized, "invalid_signature", "the signature does not match")

		return
	}

	ev := storeIncoming(appCtx, d, inbox, r, body, signatureValid)
	if ev == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot store the request")

		return
	}

	respond(d, w, r, inbox, ev)
}

// storeIncoming persists the event and publishes the notification. It returns nil only on
// a storage failure (already logged).
func storeIncoming(
	appCtx context.Context,
	d Deps,
	inbox *storage.Inbox,
	r *http.Request,
	body []byte,
	signatureValid *bool,
) *storage.Event {
	ct := r.Header.Get("Content-Type")

	ev := storage.Event{
		InboxID:        inbox.ID,
		Method:         strings.ToUpper(r.Method),
		Path:           r.URL.Path,
		Query:          r.URL.RawQuery,
		ContentType:    ct,
		Headers:        capture.Headers(r.Header, d.Cipher, inbox.ID),
		Body:           body,
		ClientIP:       d.realIP(r),
		SignatureValid: signatureValid,
		CreatedAt:      time.Now().UTC(),
	}

	if err := d.Store.CreateEvent(r.Context(), &ev); err != nil {
		d.Log.Error("cannot store captured request", zap.Error(err))

		return nil
	}

	// Publish with the application context: the request context dies as soon as we
	// respond, and subscribers would lose the event.
	go func() {
		_ = d.Pub.Publish(appCtx, inbox.ID.String(), notify.Message{
			Action: notify.ActionCreate,
			Event: notify.Event{
				ID:          ev.ID,
				InboxID:     inbox.ID,
				Method:      ev.Method,
				ContentType: ev.ContentType,
				BodySize:    ev.BodySize,
				ClientIP:    ev.ClientIP,
				CreatedAtMS: ev.CreatedAt.UnixMilli(),
			},
		})
	}()

	return &ev
}

func verifySignature(inbox *storage.Inbox, body []byte, presented string) *bool {
	if len(inbox.SigningSecret) == 0 || inbox.SignatureHeader == "" {
		return nil
	}

	valid := crypto.Verify(inbox.SigningSecret, body, presented)

	return &valid
}

func respond(d Deps, w http.ResponseWriter, r *http.Request, inbox *storage.Inbox, ev *storage.Event) {
	if inbox.ResponseDelayMS > 0 {
		sleep(r.Context(), time.Duration(inbox.ResponseDelayMS)*time.Millisecond)
	}

	// The CORS headers were set at the top of the handler so that error responses carry
	// them as well.
	w.Header().Set("X-Wh-Event-Id", ev.ID.String())

	for _, h := range inbox.ResponseHeaders {
		if capture.IsSensitive(h.Name) {
			continue // never echo a secret back
		}

		w.Header().Set(h.Name, h.Value)
	}

	status := inbox.ResponseCode
	if status < 100 || status > 599 {
		status = http.StatusOK
	}

	if len(inbox.ResponseBody) > 0 {
		w.WriteHeader(status)
		// #nosec G705 -- the body is the inbox owner's configured mock response,
		// written back byte for byte; it is not interpolated into a template.
		_, _ = w.Write(inbox.ResponseBody)

		return
	}

	// Default: the task requires the event id in the response.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	//nolint:errchkjson // the client may be gone already; a failed write has nowhere to go.
	_ = json.NewEncoder(w).Encode(struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	}{ev.ID.String(), true})
}

// The "+1" in the MaxBytesReader budget is the whole trick: a body of exactly 1 MiB must
// be accepted while 1 MiB + 1 byte must be rejected, and the extra byte is what tells
// them apart. An oversized body is never buffered in full and never stored.

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// writeError keeps the capture endpoint's error shape identical to the JSON API, so the
// UI can render third party failures (GitHub, Stripe, ...) the same way it renders ours.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	//nolint:errchkjson // the client may be gone already; a failed write has nowhere to go.
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, msg}})
}

// trustedHeaders are consulted for the client IP, lowest priority first. They are only
// trusted when the deployment is behind a proxy that rewrites them; otherwise
// X-Forwarded-For is attacker controlled, so we fall back to the socket address.
var trustedHeaders = [...]string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}

func (d Deps) realIP(r *http.Request) string {
	if !d.TrustProxy {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			return host
		}

		return r.RemoteAddr
	}

	for _, name := range trustedHeaders {
		if v := r.Header.Get(name); v != "" {
			parts := strings.Split(v, ",")
			if ip := strings.TrimSpace(parts[0]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}

	return r.RemoteAddr
}
