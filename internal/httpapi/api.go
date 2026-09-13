// Package httpapi exposes the JSON API consumed by the web UI: /api/v1/...
//
// The contract is hand written instead of generated from OpenAPI. Reason: the upstream
// project generated both the server stubs and the TypeScript client, so a fresh clone
// cannot even be compiled until a code generation step (and Node) is available. For a
// project that has to be reproducible by a reviewer we prefer one explicit source of
// truth; the DTOs below mirror web/src/api/v1.ts one to one.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/ratelimit"
	"github.com/yuandzhang/webhook-zq/internal/replay"
	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/version"
)

// Deps holds the API dependencies.
type Deps struct {
	Log      *zap.Logger
	Settings *config.AppSettings
	Store    storage.Store
	Replay   *replay.Service
	Cipher   *crypto.Cipher
	PubSub   pubsub.PubSub[notify.Message]

	// Limiter is optional: when nil, one is derived from Settings.ReplayRateLimit.
	Limiter *ratelimit.Limiter
}

// API is the router together with its dependencies.
type API struct {
	deps    Deps
	mux     *http.ServeMux
	limiter *ratelimit.Limiter
}

// New builds the API (mounted at /api, so all patterns start with /v1).
func New(d Deps) http.Handler {
	if d.Limiter == nil {
		d.Limiter = ratelimit.New(d.Settings.ReplayRateLimit, time.Minute, nil)
	}

	a := API{deps: d, mux: http.NewServeMux(), limiter: d.Limiter}

	a.mux.HandleFunc("GET /v1/health", a.health)
	a.mux.HandleFunc("GET /v1/settings", a.settings)

	a.mux.HandleFunc("GET /v1/inboxes", a.listInboxes)
	a.mux.HandleFunc("POST /v1/inboxes", a.createInbox)
	a.mux.HandleFunc("GET /v1/inboxes/{id}", a.getInbox)
	a.mux.HandleFunc("PATCH /v1/inboxes/{id}", a.patchInbox)
	a.mux.HandleFunc("DELETE /v1/inboxes/{id}", a.deleteInbox)
	a.mux.HandleFunc("POST /v1/inboxes/{id}/token/rotate", a.rotateToken)

	a.mux.HandleFunc("GET /v1/inboxes/{id}/events", a.listEvents)
	a.mux.HandleFunc("DELETE /v1/inboxes/{id}/events", a.clearEvents)
	a.mux.HandleFunc("GET /v1/inboxes/{id}/events/subscribe", a.subscribe)

	a.mux.HandleFunc("GET /v1/events/{id}", a.getEvent)
	a.mux.HandleFunc("DELETE /v1/events/{id}", a.deleteEvent)
	a.mux.HandleFunc("GET /v1/events/{id}/export", a.exportEvent)
	// Replay is the only endpoint that makes the server talk to a third party, so it is
	// the only one with a rate limit (see ratelimit package docs for the scope).
	a.mux.HandleFunc("POST /v1/events/{id}/replay", a.limited(a.createReplay))
	a.mux.HandleFunc("GET /v1/events/{id}/replays", a.listReplays)

	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown endpoint: "+r.URL.Path)
	})

	return a.auth(a.mux)
}

// tenantContextKey carries the resolved tenant through the request.
type tenantContextKey struct{}

func withTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenant)
}

// tenantFrom returns the tenant resolved by the auth middleware.
func tenantFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(tenantContextKey{}).(string)

	return v, ok && v != ""
}

// auth enforces access control and resolves the tenant.
//
// Two modes:
//   - `--auth-keys=alice:<keyA>,bob:<keyB>`: per tenant keys. The tenant owning the key is
//     stored in the request context and becomes inbox.owner_key, which is what makes the
//     multi tenant filtering real rather than decorative.
//   - `--auth-token=<key>`: one shared key, everything belongs to the default tenant.
//
// The public capture endpoint (/hooks/...) is not behind this middleware by design:
// third parties must be able to POST without credentials.
func (a API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := presentedToken(r)

		if len(a.deps.Settings.AuthKeys) > 0 {
			for tenant, key := range a.deps.Settings.AuthKeys {
				if constantTimeEqual(token, key) {
					next.ServeHTTP(w, r.WithContext(withTenant(r.Context(), tenant)))

					return
				}
			}

			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid api key")

			return
		}

		if a.deps.Settings.AuthToken == "" {
			next.ServeHTTP(w, r)

			return
		}

		if !constantTimeEqual(token, a.deps.Settings.AuthToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")

			return
		}

		next.ServeHTTP(w, r)
	})
}

// presentedToken reads the access token from the Authorization header or the HttpOnly
// cookie. A query parameter is deliberately NOT supported: URLs end up in access logs,
// browser history and Referer headers.
func presentedToken(r *http.Request) string {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return strings.TrimSpace(v[len("bearer "):])
	}

	if c, err := r.Cookie("whq_token"); err == nil {
		return c.Value
	}

	return ""
}

// constantTimeEqual compares without leaking the length or the position of the first
// differing byte.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (a API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())

		return
	}

	// Version is here and not only in /settings because a probe is the first thing an
	// operator reaches for when two machines behave differently.
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": version.Version()})
}

// SettingsDTO is the API representation of the running configuration.
//
// It exists because this payload used to be a map[string]any: nothing tied the JSON keys
// to the TypeScript type, so a field could be renamed on one side and the first anyone
// heard of it was a blank page - the help screen reads settings.replay_allow_hosts.length
// and a missing key is a TypeError, not an empty list.
//
// It mirrors web/src/api/v1.ts ServerSettings field for field. Add to both at once:
// SettingsFields in settings_test.go is what turns a mismatch into a failing test.
type SettingsDTO struct {
	MaxRequestBodySize uint32 `json:"max_request_body_size"`
	ReplayTimeoutMS    int64  `json:"replay_timeout_ms"`
	ReplayMaxPreview   int    `json:"replay_max_preview"`
	ReplayMaxRedirects int    `json:"replay_max_redirects"`
	ReplayMaxRetries   int    `json:"replay_max_retries"`
	ReplayRateLimit    int    `json:"replay_rate_limit"` // per minute, 0 = unlimited
	RetentionMaxEvents int    `json:"retention_max_events"`
	RetentionMaxDays   int    `json:"retention_max_days"`
	AuthEnabled        bool   `json:"auth_enabled"`
	EncryptionEnabled  bool   `json:"encryption_enabled"`
	PublicURLRoot      string `json:"public_url_root"`

	// Both slices always serialise as arrays, never null: the UI calls .length on them and
	// a null takes the whole page down. Enforced in newSettingsDTO, not at the call site,
	// so a future field cannot quietly reintroduce the bug.
	SensitiveHeaders []string `json:"sensitive_headers"`
	ReplayAllowHosts []string `json:"replay_allow_hosts"`

	ReplayAllowPrivate bool `json:"replay_allow_private"`

	// Present so the UI can answer "which build am I talking to" without a second call -
	// the same two values `status --json` reports.
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
}

// settings tells the UI how it is supposed to behave (limits are rendered next to the
// inputs instead of being discovered by trial and error).
func (a API) settings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, newSettingsDTO(
		a.deps.Settings,
		a.accessControlEnabled(),
		a.deps.Cipher != nil,
	))
}

// newSettingsDTO builds the payload. Every slice field is normalised here, in one place,
// so "always an array" is a property of the DTO rather than something each caller has to
// remember.
func newSettingsDTO(s *config.AppSettings, authEnabled, encryptionEnabled bool) SettingsDTO {
	return SettingsDTO{
		MaxRequestBodySize: s.MaxRequestBodySize,
		ReplayTimeoutMS:    s.ReplayTimeout.Milliseconds(),
		ReplayMaxPreview:   s.ReplayMaxPreview,
		ReplayMaxRedirects: s.ReplayMaxRedirects,
		ReplayMaxRetries:   s.ReplayMaxRetries,
		ReplayRateLimit:    s.ReplayRateLimit,
		RetentionMaxEvents: s.RetentionMaxEvents,
		RetentionMaxDays:   s.RetentionMaxDays,
		AuthEnabled:        authEnabled,
		EncryptionEnabled:  encryptionEnabled,
		PublicURLRoot:      s.PublicURLRoot,
		SensitiveHeaders:   sortedSensitiveHeaders(),
		ReplayAllowHosts:   nonNilCopy(s.ReplayAllowHosts),
		ReplayAllowPrivate: s.ReplayAllowPrivate,
		Version:            version.Version(),
		BuildTime:          version.BuildTime(),
	}
}

// nonNilCopy returns a non-nil copy of in. A nil slice marshals to null, and the UI does
// `hosts.length` - which would take the whole page down with a TypeError.
func nonNilCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)

	return out
}

// sortedSensitiveHeaders publishes the header names so the in-app guide can list them
// instead of hard coding them - a guide that contradicts the binary is worse than no
// guide at all.
func sortedSensitiveHeaders() []string {
	names := make([]string, 0, len(capture.SensitiveHeaders))
	for name := range capture.SensitiveHeaders {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// ── helpers ─────────────────────────────────────────────────────────────────

// accessControlEnabled reports whether any form of access control is configured.
//
// Both modes protect the API, and only with protection does it make sense to hand out
// decrypted secrets: --auth-token (one shared key) or --auth-keys (per tenant).
func (a API) accessControlEnabled() bool {
	return a.deps.Settings.AuthToken != "" || len(a.deps.Settings.AuthKeys) > 0
}

// truthy reads a boolean query parameter.
//
// The UI serialises booleans as "true" while hand written URLs and scripts use "1";
// accepting only one of them makes the toggle silently do nothing, which is exactly the
// kind of bug that only shows up in a demo.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// limited wraps a handler with the per-caller replay budget.
func (a API) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, wait := a.limiter.Allow(a.limitKey(r))
		if ok {
			next(w, r)

			return
		}

		secs := int(wait/time.Second) + 1

		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "rate_limited",
			"too many replays, retry in "+strconv.Itoa(secs)+"s")
	}
}

// limitKey prefers the authenticated tenant (a key is a stronger identity than an IP)
// and falls back to the peer address. X-Forwarded-For is deliberately ignored: trusting
// it without TrustProxy would let a caller mint unlimited identities.
func (a API) limitKey(r *http.Request) string {
	if tenant, ok := tenantFrom(r.Context()); ok && tenant != "" {
		return "tenant:" + tenant
	}

	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	return "ip:" + host
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// Headers are already flushed; the only thing left is to stop. Callers log.
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func (a API) writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, storage.ErrInboxNotFound):
		writeError(w, http.StatusNotFound, "inbox_not_found", "inbox does not exist")
	case errors.Is(err, storage.ErrEventNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "event does not exist")
	case errors.Is(err, storage.ErrTokenTaken):
		writeError(w, http.StatusConflict, "token_taken", "token already exists")
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
	default:
		// 5xx must leave a trace: the client only sees a generic message.
		a.deps.Log.Error("storage error", zap.Error(err), zap.String("context", fallback))

		writeError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}

func parseUUID(v string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(v))
}

// paging reads limit/offset with sane bounds; the UI never has to guess them.
func (a API) paging(r *http.Request, def int) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = def
	}

	max := a.deps.Settings.MaxPageSize
	if max <= 0 {
		max = config.DefaultMaxPageSize
	}

	if limit > max {
		limit = max
	}

	if offset < 0 {
		offset = 0
	}

	return limit, offset
}

func parseTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}

	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			utc := t.UTC()

			return &utc, nil
		}
	}

	return nil, errors.New("cannot parse time: " + v)
}

// defaultOwnerKey is the tenant used when access control is disabled: a single tenant
// deployment where everything belongs to the same owner.
const defaultOwnerKey = "default"

// ownerKey is the tenant of the current request: the one resolved from the API key, or
// "default" when access control is disabled (single tenant deployment).
func (a API) ownerKey(r *http.Request) string {
	if tenant, ok := tenantFrom(r.Context()); ok {
		return tenant
	}

	return defaultOwnerKey
}

// ownedInbox loads an inbox and verifies it belongs to the caller.
//
// Listing an inbox is filtered by tenant, but every endpoint that takes an id has to be
// checked as well - otherwise knowing (or guessing) a UUID would be enough to read another
// tenant's data. A foreign inbox is reported as 404, not 403: the fact that it exists is
// itself information.
func (a API) ownedInbox(w http.ResponseWriter, r *http.Request, id uuid.UUID) (*storage.Inbox, bool) {
	in, err := a.deps.Store.GetInbox(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err, "cannot read inbox")

		return nil, false
	}

	if in.OwnerKey != a.ownerKey(r) {
		writeError(w, http.StatusNotFound, "not_found", "inbox does not exist")

		return nil, false
	}

	return in, true
}

// ownedEvent loads an event and verifies its inbox belongs to the caller.
func (a API) ownedEvent(w http.ResponseWriter, r *http.Request, id uuid.UUID) (*storage.Event, bool) {
	ev, err := a.deps.Store.GetEvent(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err, "cannot read event")

		return nil, false
	}

	in, err := a.deps.Store.GetInbox(r.Context(), ev.InboxID)
	if err != nil || in.OwnerKey != a.ownerKey(r) {
		writeError(w, http.StatusNotFound, "not_found", "event does not exist")

		return nil, false
	}

	return ev, true
}

// maxBody guards the JSON payloads of the management API (not the capture endpoint, which
// has its own streaming limit).
const maxBody = 1 << 20

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()

	return dec.Decode(dst)
}
