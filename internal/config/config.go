// Package config holds the application runtime settings.
//
// Design note: every field here is either (a) required for correctness of a business rule
// documented in the README (body limit, replay timeout, target policy, retention) or
// (b) an explicitly optional hardening switch (auth token, encryption key). Nothing is
// kept "just in case" - the upstream project had ~20 flags, most of them alternative
// storage/pubsub drivers that we deliberately removed.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/yuandzhang/webhook-zq/internal/replay"
)

// Defaults. They are the single source of truth for both the CLI help and the README.
const (
	DefaultMaxRequestBodySize = 1 << 20 // 1 MiB, per the task requirement
	DefaultReplayTimeout      = 10 * time.Second
	DefaultReplayMaxPreview   = 4096 // bytes of the response body we keep
	DefaultReplayMaxRedirects = 0    // redirects are disabled by default
	DefaultReplayMaxRetries   = 0    // retries are opt-in
	DefaultReplayBackoff      = 500 * time.Millisecond
	DefaultRetentionMaxEvents = 500
	DefaultRetentionMaxDays   = 30
	DefaultRetentionInterval  = 10 * time.Minute
	DefaultMaxPageSize        = 100
	DefaultReplayRateLimit    = 30 // replays per minute per caller; 0 disables the limit
)

// AppSettings is the assembled runtime configuration.
type AppSettings struct {
	// PublicURLRoot is an optional override for the receive URL shown in the UI
	// (e.g. https://webhook.example.com). Empty means "derive from the browser location".
	PublicURLRoot string

	// Capture
	MaxRequestBodySize uint32 // hard limit for POST /hooks/{token}

	// Replay
	ReplayTimeout      time.Duration
	ReplayMaxPreview   int
	ReplayMaxRedirects int
	ReplayMaxRetries   int
	ReplayBackoff      time.Duration
	ReplayAllowHosts   []string // explicit allow-list, empty = rely on the IP policy only
	ReplayAllowPrivate bool     // allow listed hosts may resolve to reserved addresses (demos only)

	// ReplayRateLimit caps how many replays one caller may start per minute. A replay
	// makes the server issue outbound requests, so it is the one endpoint that can be
	// used to bother third parties - it gets a limit, the read endpoints do not.
	ReplayRateLimit int

	// Retention
	RetentionMaxEvents int
	RetentionMaxDays   int
	RetentionInterval  time.Duration

	// Hardening (all optional)
	AuthToken string // single shared token; when set, UI + /api/v1 require it (/hooks/* stays public)

	// AuthKeys maps a tenant name to its API key ("alice:<key>,bob:<key>"). This is what
	// makes multi tenancy work: the tenant resolved from the key becomes inbox.owner_key,
	// so two callers with different keys simply do not see each other's inboxes.
	AuthKeys map[string]string

	EncryptKey string // base64 32-byte key; when set, sensitive headers are stored encrypted

	// TrustProxy allows the client IP to be read from X-Forwarded-For & friends. Only
	// enable it behind a proxy that rewrites those headers, otherwise the recorded source
	// IP is attacker controlled.
	TrustProxy bool

	// Pagination
	MaxPageSize int
}

// Validate catches configuration mistakes at startup instead of at first request.
func (s AppSettings) Validate() error {
	if s.MaxRequestBodySize == 0 {
		return errors.New("max request body size must be greater than zero")
	}

	if s.ReplayTimeout <= 0 {
		return errors.New("replay timeout must be greater than zero")
	}

	if s.ReplayMaxPreview <= 0 {
		return errors.New("replay max preview must be greater than zero")
	}

	if s.ReplayMaxRedirects < 0 {
		return errors.New("replay max redirects cannot be negative")
	}

	if s.ReplayMaxRetries < 0 || s.ReplayMaxRetries > 5 {
		return errors.New("replay max retries must be between 0 and 5")
	}

	if s.ReplayRateLimit < 0 {
		return errors.New("replay rate limit cannot be negative (0 disables it)")
	}

	if s.RetentionMaxEvents < 0 || s.RetentionMaxDays < 0 {
		return errors.New("retention values cannot be negative")
	}

	if s.RetentionInterval <= 0 {
		return errors.New("retention interval must be greater than zero")
	}

	if s.MaxPageSize <= 0 || s.MaxPageSize > 500 {
		return errors.New("max page size must be between 1 and 500")
	}

	if s.EncryptKey != "" {
		if err := ValidateKey(s.EncryptKey); err != nil {
			return fmt.Errorf("invalid encrypt key: %w", err)
		}
	}

	for name, key := range s.AuthKeys {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(key) == "" {
			return errors.New("auth keys must have the form <tenant>:<key>")
		}
	}

	if s.PublicURLRoot != "" {
		u, err := url.Parse(s.PublicURLRoot)
		if err != nil {
			return fmt.Errorf("invalid public url root: %w", err)
		}

		if u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("public url root must use http or https")
		}

		if u.User != nil || u.Host == "" {
			return errors.New("public url root must contain a host and no credentials")
		}
	}

	// An allow host that is itself a reserved address can never be reached unless
	// AllowPrivate is on. Fail at startup instead of silently ignoring the setting.
	for _, h := range s.ReplayAllowHosts {
		if ip := net.ParseIP(strings.TrimSpace(h)); ip != nil && replay.BlockedIP(ip) && !s.ReplayAllowPrivate {
			return fmt.Errorf(
				"replay allow host %q is a reserved address: also enable --replay-allow-private (demos only)", h)
		}
	}

	return nil
}
