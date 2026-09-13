// Package storage defines the persistence contract of the application.
//
// Why it looks the way it does:
//
//   - The upstream project modelled a "session" as an *ephemeral* thing with a TTL and a
//     hard cap of 128 captured requests. That is fine for a throwaway debug tool but wrong
//     for us: the task requires durable inboxes, paging and filtering. So the model is
//     Inbox -> Event -> ReplayAttempt, and every list operation is executed in SQL.
//   - The interface is deliberately small and *total*: no method returns "not supported".
//     There is one production driver (PostgreSQL) and one in-memory driver used by tests.
//   - Bodies are []byte end to end. Pretty printing is a presentation concern only; we
//     never store a re-encoded body, otherwise replay would send different bytes than the
//     ones that were received.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors. Handlers map them to HTTP codes, so they must stay stable.
var (
	ErrNotFound        = errors.New("not found")
	ErrInboxNotFound   = errors.New("inbox not found")
	ErrEventNotFound   = errors.New("event not found")
	ErrTokenTaken      = errors.New("inbox token already exists")
	ErrInvalidArgument = errors.New("invalid argument")
)

// Outcome values for a replay attempt.
const (
	OutcomeSuccess      = "success"       // 2xx
	OutcomeHTTPError    = "http_error"    // got a response, but not 2xx
	OutcomeTimeout      = "timeout"       // client timeout or context deadline
	OutcomeNetworkError = "network_error" // dial/DNS/TLS failure
	OutcomeBlocked      = "blocked"       // rejected by the target policy, request never sent
)

// Inbox is a webhook receive endpoint owned by the user.
type Inbox struct {
	ID         uuid.UUID `json:"id"`
	OwnerKey   string    `json:"owner_key"`
	Name       string    `json:"name"`
	Token      string    `json:"token"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	EventCount int       `json:"event_count,omitempty"` // populated by list queries only
	LastEventAt *time.Time `json:"last_event_at,omitempty"`

	// Custom response returned to the webhook sender (a debugging aid, not required by
	// the task, but extremely useful to simulate 500s and slow endpoints).
	ResponseCode     int          `json:"response_code"`
	ResponseHeaders  []HttpHeader `json:"response_headers"`
	ResponseBody     []byte       `json:"-"`
	ResponseDelayMS  int          `json:"response_delay_ms"`

	// Signing (optional feature).
	SigningSecret    []byte `json:"-"`                  // plaintext, encrypted at rest
	SignatureHeader  string `json:"signature_header"`   // e.g. X-Signature
	SignatureScheme  string `json:"signature_scheme"`   // hmac-sha256-hex | sha256-prefixed
	RequireSignature bool   `json:"require_signature"`  // reject (401) when invalid

	// Retention (optional feature, defaults applied on create).
	RetentionMaxEvents int `json:"retention_max_events"`
	RetentionMaxDays   int `json:"retention_max_days"`
}

// Event is a captured webhook request.
type Event struct {
	ID           uuid.UUID  `json:"id"`
	InboxID      uuid.UUID  `json:"inbox_id"`
	Method       string     `json:"method"`
	Path         string     `json:"path"`
	Query        string     `json:"query"`
	ContentType  string     `json:"content_type"`
	Headers      []HttpHeader `json:"headers"`
	Body         []byte     `json:"-"`
	// BodyText is a searchable mirror of Body. It is only filled when the body is valid
	// UTF-8 without NUL bytes; binary payloads stay empty and are simply not searchable.
	// Never used for display or replay - those always read Body.
	BodyText     string     `json:"-"`
	BodySize     int        `json:"body_size"`
	ClientIP     string     `json:"client_ip"`
	SignatureValid *bool    `json:"signature_valid"` // nil = not checked
	CreatedAt    time.Time  `json:"created_at"`
}

// ReplayAttempt is one execution of "send this event to that URL".
type ReplayAttempt struct {
	ID               uuid.UUID  `json:"id"`
	EventID          uuid.UUID  `json:"event_id"`
	InboxID          uuid.UUID  `json:"inbox_id"`
	AttemptNo        int        `json:"attempt_no"`
	RetryOf          *uuid.UUID `json:"retry_of,omitempty"`
	TargetURL        string     `json:"target_url"`
	EditedInput      *EditedInput `json:"edited_input,omitempty"`
	SignApplied      bool       `json:"sign_applied"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMS       int        `json:"duration_ms"`
	StatusCode       *int       `json:"status_code,omitempty"`
	ResponsePreview  string     `json:"response_preview,omitempty"`
	PreviewTruncated bool       `json:"preview_truncated"`
	Error            string     `json:"error,omitempty"`
	Outcome          string     `json:"outcome"`
	CreatedAt        time.Time  `json:"created_at"`
}

// EditedInput captures what the user changed when replaying an edited request. It is
// stored (truncated) so a replay is reproducible and auditable; the original event is
// never modified.
type EditedInput struct {
	Method  string       `json:"method,omitempty"`
	Headers []HttpHeader `json:"headers,omitempty"`
	Body    string       `json:"body,omitempty"`
}

// HttpHeader is a single HTTP header. `Sensitive` marks values that are stored masked or
// encrypted and must not be forwarded on replay.
type HttpHeader struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// InboxFilter is the query for the inbox list.
type InboxFilter struct {
	OwnerKey string
	Query    string // substring match on name
	Enabled  *bool

	// AllOwners skips the tenant filter. It exists for internal sweeps (the retention
	// cleaner has to walk every inbox) and must never be set from a request handler:
	// the default is "filter by owner", so forgetting it fails closed.
	AllOwners bool
	Limit    int
	Offset   int
}

// EventFilter is the query for the event list. All conditions are combined with AND.
type EventFilter struct {
	InboxID     uuid.UUID
	From        *time.Time
	To          *time.Time
	ContentType string
	Query       string // substring match on the decoded body
	Method      string
	Limit       int
	Offset      int
}

// EventListItem is an event plus the replay summary needed by the list view, so the UI
// can show "replayed 3x, last 500" without N+1 queries.
//
// It intentionally does NOT carry the body: a page of 100 events would otherwise transfer
// up to 100 MiB. `Preview` holds the first characters of the searchable text mirror, and
// the full body is fetched only when the user opens one event.
type EventListItem struct {
	Event
	Preview       string     `json:"preview"`
	ReplayCount   int        `json:"replay_count"`
	LastOutcome   string     `json:"last_outcome,omitempty"`
	LastStatus    *int       `json:"last_status_code,omitempty"`
	LastReplayedAt *time.Time `json:"last_replayed_at,omitempty"`
}

// InboxPatch is a partial update. Nil/zero fields are left untouched; `SetX` flags make
// the intent explicit so that "disable" is distinguishable from "not mentioned".
type InboxPatch struct {
	Name       *string
	Enabled    *bool
	SigningSecret    *[]byte
	SignatureHeader  *string
	SignatureScheme  *string
	RequireSignature *bool
	RetentionMaxEvents *int
	RetentionMaxDays   *int
	// Custom response returned to the webhook sender.
	ResponseCode     *int
	ResponseDelayMS  *int
	ResponseBody     *[]byte
}

// Store is the full persistence contract.
type Store interface {
	// Inboxes
	CreateInbox(ctx context.Context, in *Inbox) error
	GetInbox(ctx context.Context, id uuid.UUID) (*Inbox, error)
	GetInboxByToken(ctx context.Context, token string) (*Inbox, error)
	ListInboxes(ctx context.Context, f InboxFilter) ([]Inbox, int, error)
	UpdateInbox(ctx context.Context, id uuid.UUID, p InboxPatch) error
	RotateToken(ctx context.Context, id uuid.UUID) (string, error)
	DeleteInbox(ctx context.Context, id uuid.UUID) error

	// Events
	CreateEvent(ctx context.Context, e *Event) error
	GetEvent(ctx context.Context, id uuid.UUID) (*Event, error)
	ListEvents(ctx context.Context, f EventFilter) ([]EventListItem, int, error)
	DeleteEvent(ctx context.Context, id uuid.UUID) error
	// DeleteInboxEvents removes every event of an inbox (used by "clear"). Replay
	// attempts go away through the foreign key, so this is a single statement.
	DeleteInboxEvents(ctx context.Context, inboxID uuid.UUID) (int64, error)
	// PruneEvents enforces retention and returns the number of deleted rows.
	PruneEvents(ctx context.Context, inboxID uuid.UUID, maxEvents, maxDays int) (int64, error)

	// Replays
	CreateReplay(ctx context.Context, a *ReplayAttempt) error
	ListReplays(ctx context.Context, eventID uuid.UUID, limit int) ([]ReplayAttempt, error)

	// Lifecycle
	Ping(ctx context.Context) error
	Close() error
}
