// Package postgres is the production storage driver.
//
// Notes on the SQL:
//   - Every list query filters by inbox_id first and reuses (inbox_id, created_at DESC),
//     so paging stays index-only as the tables grow.
//   - The event list joins the latest replay attempt with LATERAL instead of issuing N+1
//     queries for the "replayed? last result?" columns shown in the UI.
//   - Bodies are bytea and are never decoded/re-encoded on the way in or out.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// Store implements storage.Store on top of PostgreSQL.
type Store struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher // optional; when nil, secrets are kept masked, never in the clear
}

var _ storage.Store = (*Store)(nil)

// New creates a driver. pool must be non-nil. cipher may be nil (see Config.EncryptKey).
func New(pool *pgxpool.Pool, cipher *crypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// Pool exposes the underlying pool (used by the readiness probe).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close releases the connections.
func (s *Store) Close() error {
	s.pool.Close()

	return nil
}

// ── inboxes ─────────────────────────────────────────────────────────────────

const inboxColumns = `id, owner_key, name, token, enabled, response_code, response_headers,
	response_body, response_delay_ms, signing_secret_enc, signature_header, signature_scheme,
	require_signature, retention_max_events, retention_max_days, created_at, updated_at`

// CreateInbox inserts a new inbox. ID, token and timestamps must be set by the caller;
// the database assigns nothing except defaults, which keeps the API layer in control of
// the token lifecycle (and lets it retry on the unique violation).
func (s *Store) CreateInbox(ctx context.Context, in *storage.Inbox) error {
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}

	headers, err := json.Marshal(in.ResponseHeaders)
	if err != nil {
		return fmt.Errorf("cannot encode response headers: %w", err)
	}

	secret, err := s.encryptSecret(in.ID, in.SigningSecret)
	if err != nil {
		return err
	}

	// A nil []byte is sent as SQL NULL by pgx; the column is NOT NULL, so normalise it.
	body := in.ResponseBody
	if body == nil {
		body = []byte{}
	}

	now := time.Now().UTC()

	_, err = s.pool.Exec(ctx, `INSERT INTO inbox (`+inboxColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
		$10,$11,$12,$13,$14,$15,$16,$17)`,
		in.ID, in.OwnerKey, in.Name, in.Token, in.Enabled,
		in.ResponseCode, headers, body, in.ResponseDelayMS,
		secret, in.SignatureHeader, in.SignatureScheme, in.RequireSignature,
		in.RetentionMaxEvents, in.RetentionMaxDays, now, now,
	)
	if err != nil {
		if isUniqueViolation(err, "inbox_token_key") {
			return storage.ErrTokenTaken
		}

		return fmt.Errorf("cannot create inbox: %w", err)
	}

	in.CreatedAt, in.UpdatedAt = now, now

	return nil
}

func (s *Store) scanInbox(row pgx.Row) (*storage.Inbox, error) {
	var (
		in      storage.Inbox
		headers []byte
		secret  []byte
	)

	err := row.Scan(
		&in.ID, &in.OwnerKey, &in.Name, &in.Token, &in.Enabled,
		&in.ResponseCode, &headers, &in.ResponseBody, &in.ResponseDelayMS,
		&secret, &in.SignatureHeader, &in.SignatureScheme, &in.RequireSignature,
		&in.RetentionMaxEvents, &in.RetentionMaxDays, &in.CreatedAt, &in.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrInboxNotFound
		}

		return nil, fmt.Errorf("cannot read inbox: %w", err)
	}

	if len(headers) > 0 {
		if err = json.Unmarshal(headers, &in.ResponseHeaders); err != nil {
			return nil, fmt.Errorf("cannot decode response headers: %w", err)
		}
	}

	if in.SigningSecret, err = s.decryptSecret(in.ID, secret); err != nil {
		return nil, err
	}

	return &in, nil
}

// GetInbox returns an inbox by id.
func (s *Store) GetInbox(ctx context.Context, id uuid.UUID) (*storage.Inbox, error) {
	return s.scanInbox(s.pool.QueryRow(ctx, `SELECT `+inboxColumns+` FROM inbox WHERE id = $1`, id))
}

// GetInboxByToken resolves the public receive token. This is the hot path (every inbound
// webhook) and is covered by the unique index on token.
func (s *Store) GetInboxByToken(ctx context.Context, token string) (*storage.Inbox, error) {
	if !storage.IsValidToken(token) {
		return nil, storage.ErrInboxNotFound
	}

	return s.scanInbox(s.pool.QueryRow(ctx, `SELECT `+inboxColumns+` FROM inbox WHERE token = $1`, token))
}

// ListInboxes returns a page of inboxes with their event counters.
func (s *Store) ListInboxes(ctx context.Context, f storage.InboxFilter) ([]storage.Inbox, int, error) {
	var (
		where []string
		args  []any
		idx   = 1
	)

	// The tenant filter is on by default; only an explicit AllOwners (internal sweeps)
	// turns it off. A caller that forgets to set OwnerKey therefore sees nothing rather
	// than everything.
	if !f.AllOwners {
		where = append(where, fmt.Sprintf("i.owner_key = $%d", idx))
		args = append(args, f.OwnerKey)
		idx++
	}

	if f.Query != "" {
		where = append(where, fmt.Sprintf("i.name ILIKE $%d", idx))
		args = append(args, "%"+f.Query+"%")
		idx++
	}

	if f.Enabled != nil {
		where = append(where, fmt.Sprintf("i.enabled = $%d", idx))
		args = append(args, *f.Enabled)
		idx++
	}

	// With AllOwners there may be no predicate at all; "WHERE " with nothing after it is
	// a syntax error, so the keyword is part of the condition.
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM inbox i`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("cannot count inboxes: %w", err)
	}

	args = append(args, f.Limit, f.Offset)

	rows, err := s.pool.Query(ctx, `SELECT `+withAlias(inboxColumns, "i")+`,
			coalesce(st.cnt, 0) AS event_count, st.last_at AS last_event_at
		FROM inbox i
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS cnt, max(created_at) AS last_at FROM event WHERE inbox_id = i.id
		) st ON true`+cond+`
		ORDER BY i.created_at DESC, i.id DESC
		LIMIT $`+itoa(idx)+` OFFSET $`+itoa(idx+1), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot list inboxes: %w", err)
	}

	defer rows.Close()

	var out []storage.Inbox

	for rows.Next() {
		var (
			in      storage.Inbox
			headers []byte
			secret  []byte
		)

		err = rows.Scan(
			&in.ID, &in.OwnerKey, &in.Name, &in.Token, &in.Enabled,
			&in.ResponseCode, &headers, &in.ResponseBody, &in.ResponseDelayMS,
			&secret, &in.SignatureHeader, &in.SignatureScheme, &in.RequireSignature,
			&in.RetentionMaxEvents, &in.RetentionMaxDays, &in.CreatedAt, &in.UpdatedAt,
			&in.EventCount, &in.LastEventAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("cannot read inbox: %w", err)
		}

		if len(headers) > 0 {
			if err = json.Unmarshal(headers, &in.ResponseHeaders); err != nil {
				return nil, 0, fmt.Errorf("cannot decode response headers: %w", err)
			}
		}

		if in.SigningSecret, err = s.decryptSecret(in.ID, secret); err != nil {
			return nil, 0, err
		}

		out = append(out, in)
	}

	return out, total, rows.Err()
}

// UpdateInbox applies a partial patch.
//
// One UPDATE that only touches the columns present in the patch. The previous version
// read the row, merged it in Go and wrote every column back - two concurrent patches
// (say, "rename" and "disable") could silently undo each other, and an unrelated patch
// still rewrote the signing secret.
func (s *Store) UpdateInbox(ctx context.Context, id uuid.UUID, p storage.InboxPatch) error {
	var (
		sets []string
		args []any
	)

	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if p.Name != nil {
		add("name", *p.Name)
	}

	if p.Enabled != nil {
		add("enabled", *p.Enabled)
	}

	if p.SignatureHeader != nil {
		add("signature_header", *p.SignatureHeader)
	}

	if p.SignatureScheme != nil {
		add("signature_scheme", *p.SignatureScheme)
	}

	if p.RequireSignature != nil {
		add("require_signature", *p.RequireSignature)
	}

	if p.RetentionMaxEvents != nil {
		add("retention_max_events", *p.RetentionMaxEvents)
	}

	if p.RetentionMaxDays != nil {
		add("retention_max_days", *p.RetentionMaxDays)
	}

	if p.ResponseCode != nil {
		add("response_code", *p.ResponseCode)
	}

	if p.ResponseDelayMS != nil {
		add("response_delay_ms", *p.ResponseDelayMS)
	}

	if p.ResponseBody != nil {
		body := *p.ResponseBody
		if body == nil {
			body = []byte{}
		}

		add("response_body", body)
	}

	if p.SigningSecret != nil {
		// An explicitly empty secret clears the stored one; a non-empty one is encrypted
		// with the master key before it ever reaches the database.
		secret, err := s.encryptSecret(id, *p.SigningSecret)
		if err != nil {
			return err
		}

		add("signing_secret_enc", secret)
	}

	if len(sets) == 0 {
		// Nothing to change - still answer honestly about whether the row exists.
		_, err := s.GetInbox(ctx, id)

		return err
	}

	args = append(args, id)

	q := fmt.Sprintf("UPDATE inbox SET %s, updated_at = now() WHERE id = $%d",
		strings.Join(sets, ", "), len(args))

	res, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("cannot update inbox: %w", err)
	}

	if res.RowsAffected() == 0 {
		return storage.ErrInboxNotFound
	}

	return nil
}

// RotateToken replaces the receive token. Old URLs stop working immediately - that is the
// point: it is the remediation action when a URL leaked.
func (s *Store) RotateToken(ctx context.Context, id uuid.UUID) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		token, err := storage.NewToken()
		if err != nil {
			return "", err
		}

		res, err := s.pool.Exec(ctx, `UPDATE inbox SET token = $2, updated_at = now() WHERE id = $1`, id, token)
		if err != nil {
			if isUniqueViolation(err, "inbox_token_key") {
				continue // astronomically unlikely; retry with a new random token
			}

			return "", fmt.Errorf("cannot rotate token: %w", err)
		}

		if res.RowsAffected() == 0 {
			return "", storage.ErrInboxNotFound
		}

		return token, nil
	}

	return "", storage.ErrTokenTaken
}

// DeleteInbox removes the inbox and (via ON DELETE CASCADE) its events and replays.
func (s *Store) DeleteInbox(ctx context.Context, id uuid.UUID) error {
	res, err := s.pool.Exec(ctx, `DELETE FROM inbox WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("cannot delete inbox: %w", err)
	}

	if res.RowsAffected() == 0 {
		return storage.ErrInboxNotFound
	}

	return nil
}

// ── events ──────────────────────────────────────────────────────────────────

// CreateEvent stores a captured request.
func (s *Store) CreateEvent(ctx context.Context, e *storage.Event) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}

	headers, err := json.Marshal(e.Headers)
	if err != nil {
		return fmt.Errorf("cannot encode headers: %w", err)
	}

	e.BodySize = len(e.Body)

	// Same NULL normalisation as for the inbox response body.
	if e.Body == nil {
		e.Body = []byte{}
	}

	e.BodyText = storage.SearchableText(e.Body)

	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}

	_, err = s.pool.Exec(ctx, `INSERT INTO event
		(id, inbox_id, method, path, query, content_type, headers, body, body_text, body_size, client_ip, signature_valid, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		e.ID, e.InboxID, e.Method, e.Path, e.Query, e.ContentType, headers,
		e.Body, e.BodyText, e.BodySize, e.ClientIP, e.SignatureValid, e.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("cannot create event: %w", err)
	}

	return nil
}

// GetEvent returns one event with its body.
func (s *Store) GetEvent(ctx context.Context, id uuid.UUID) (*storage.Event, error) {
	var (
		e       storage.Event
		headers []byte
	)

	err := s.pool.QueryRow(ctx, `SELECT id, inbox_id, method, path, query, content_type, headers,
		body, body_text, body_size, client_ip, signature_valid, created_at FROM event WHERE id = $1`, id).
		Scan(&e.ID, &e.InboxID, &e.Method, &e.Path, &e.Query, &e.ContentType, &headers,
			&e.Body, &e.BodyText, &e.BodySize, &e.ClientIP, &e.SignatureValid, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrEventNotFound
		}

		return nil, fmt.Errorf("cannot read event: %w", err)
	}

	if len(headers) > 0 {
		if err = json.Unmarshal(headers, &e.Headers); err != nil {
			return nil, fmt.Errorf("cannot decode headers: %w", err)
		}
	}

	return &e, nil
}

// ListEvents returns a page of events for one inbox, newest first, with the replay summary.
func (s *Store) ListEvents(ctx context.Context, f storage.EventFilter) ([]storage.EventListItem, int, error) {
	var (
		where = []string{"e.inbox_id = $1"}
		args  = []any{f.InboxID}
		idx   = 2
	)

	if f.From != nil {
		where = append(where, fmt.Sprintf("e.created_at >= $%d", idx))
		args = append(args, *f.From)
		idx++
	}

	if f.To != nil {
		where = append(where, fmt.Sprintf("e.created_at <= $%d", idx))
		args = append(args, *f.To)
		idx++
	}

	if f.ContentType != "" {
		// Real webhooks send "application/json; charset=utf-8"; compare media types only,
		// on both sides, so a filter value with parameters still matches.
		where = append(where, fmt.Sprintf("lower(split_part(e.content_type, ';', 1)) = lower(split_part($%d, ';', 1))", idx))
		args = append(args, f.ContentType)
		idx++
	}

	if f.Method != "" {
		where = append(where, fmt.Sprintf("e.method = $%d", idx))
		args = append(args, f.Method)
		idx++
	}

	if f.Query != "" {
		// Searches the pre-computed text mirror. Converting the raw bytes here would raise
		// "invalid byte sequence" for binary payloads and fail the whole request.
		where = append(where, fmt.Sprintf("e.body_text ILIKE $%d", idx))
		args = append(args, "%"+f.Query+"%")
		idx++
	}

	cond := strings.Join(where, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM event e WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("cannot count events: %w", err)
	}

	args = append(args, f.Limit, f.Offset)

	rows, err := s.pool.Query(ctx, `SELECT e.id, e.inbox_id, e.method, e.path, e.query, e.content_type,
			e.headers, left(e.body_text, 512) AS preview, e.body_size, e.client_ip, e.signature_valid, e.created_at,
			coalesce(rp.cnt, 0) AS replay_count,
			coalesce(lr.outcome, '') AS last_outcome, lr.status_code, lr.created_at AS last_replayed_at
		FROM event e
		LEFT JOIN LATERAL (SELECT count(*)::int AS cnt FROM replay_attempt WHERE event_id = e.id) rp ON true
		LEFT JOIN LATERAL (SELECT outcome, status_code, created_at FROM replay_attempt
			WHERE event_id = e.id ORDER BY created_at DESC LIMIT 1) lr ON true
		WHERE `+cond+`
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $`+itoa(idx)+` OFFSET $`+itoa(idx+1), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot list events: %w", err)
	}

	defer rows.Close()

	var out []storage.EventListItem

	for rows.Next() {
		var (
			item    storage.EventListItem
			headers []byte
		)

		err = rows.Scan(
			&item.ID, &item.InboxID, &item.Method, &item.Path, &item.Query, &item.ContentType,
			&headers, &item.Preview, &item.BodySize, &item.ClientIP, &item.SignatureValid,
			&item.CreatedAt,
			&item.ReplayCount, &item.LastOutcome, &item.LastStatus, &item.LastReplayedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("cannot read event: %w", err)
		}

		if len(headers) > 0 {
			if err = json.Unmarshal(headers, &item.Headers); err != nil {
				return nil, 0, fmt.Errorf("cannot decode headers: %w", err)
			}
		}

		out = append(out, item)
	}

	return out, total, rows.Err()
}

// DeleteEvent removes a single event.
func (s *Store) DeleteEvent(ctx context.Context, id uuid.UUID) error {
	res, err := s.pool.Exec(ctx, `DELETE FROM event WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("cannot delete event: %w", err)
	}

	if res.RowsAffected() == 0 {
		return storage.ErrEventNotFound
	}

	return nil
}

// PruneEvents enforces the retention policy for one inbox and returns the deleted count.
// DeleteInboxEvents removes all events of an inbox in one statement. Replay attempts are
// removed by the foreign key (ON DELETE CASCADE), so there is no N+1 here.
func (s *Store) DeleteInboxEvents(ctx context.Context, inboxID uuid.UUID) (int64, error) {
	res, err := s.pool.Exec(ctx, `DELETE FROM event WHERE inbox_id = $1`, inboxID)
	if err != nil {
		return 0, fmt.Errorf("cannot delete events: %w", err)
	}

	return res.RowsAffected(), nil
}

func (s *Store) PruneEvents(ctx context.Context, inboxID uuid.UUID, maxEvents, maxDays int) (int64, error) {
	var deleted int64

	if maxDays > 0 {
		res, err := s.pool.Exec(ctx, `DELETE FROM event WHERE inbox_id = $1 AND created_at < now() - ($2 || ' days')::interval`,
			inboxID, itoa(maxDays))
		if err != nil {
			return deleted, fmt.Errorf("cannot prune by age: %w", err)
		}

		deleted += res.RowsAffected()
	}

	if maxEvents > 0 {
		res, err := s.pool.Exec(ctx, `DELETE FROM event WHERE id IN (
			SELECT id FROM event WHERE inbox_id = $1
			ORDER BY created_at DESC, id DESC OFFSET $2)`, inboxID, maxEvents)
		if err != nil {
			return deleted, fmt.Errorf("cannot prune by count: %w", err)
		}

		deleted += res.RowsAffected()
	}

	return deleted, nil
}

// ── replays ─────────────────────────────────────────────────────────────────

// CreateReplay stores one replay attempt.
func (s *Store) CreateReplay(ctx context.Context, a *storage.ReplayAttempt) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}

	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}

	if a.AttemptNo == 0 {
		a.AttemptNo = 1
	}

	var edited any
	if a.EditedInput != nil {
		raw, err := json.Marshal(a.EditedInput)
		if err != nil {
			return fmt.Errorf("cannot encode edited input: %w", err)
		}

		edited = raw
	}

	_, err := s.pool.Exec(ctx, `INSERT INTO replay_attempt
		(id, event_id, inbox_id, attempt_no, retry_of, target_url, edited_input, sign_applied,
		 started_at, finished_at, duration_ms, status_code, response_preview, preview_truncated,
		 error, outcome, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		a.ID, a.EventID, a.InboxID, a.AttemptNo, a.RetryOf, a.TargetURL, edited, a.SignApplied,
		a.StartedAt, a.FinishedAt, a.DurationMS, a.StatusCode, a.ResponsePreview,
		a.PreviewTruncated, nullIfEmpty(a.Error), a.Outcome, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("cannot store replay attempt: %w", err)
	}

	return nil
}

// ListReplays returns the attempts for one event, newest first.
func (s *Store) ListReplays(ctx context.Context, eventID uuid.UUID, limit int) ([]storage.ReplayAttempt, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx, `SELECT id, event_id, inbox_id, attempt_no, retry_of, target_url,
		edited_input, sign_applied, started_at, finished_at, duration_ms, status_code,
		response_preview, preview_truncated, coalesce(error, ''), outcome, created_at
		FROM replay_attempt WHERE event_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("cannot list replays: %w", err)
	}

	defer rows.Close()

	var out []storage.ReplayAttempt

	for rows.Next() {
		var (
			a       storage.ReplayAttempt
			edited  []byte
			errText string
		)

		err = rows.Scan(&a.ID, &a.EventID, &a.InboxID, &a.AttemptNo, &a.RetryOf, &a.TargetURL,
			&edited, &a.SignApplied, &a.StartedAt, &a.FinishedAt, &a.DurationMS, &a.StatusCode,
			&a.ResponsePreview, &a.PreviewTruncated, &errText, &a.Outcome, &a.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("cannot read replay attempt: %w", err)
		}

		if len(edited) > 0 {
			var ei storage.EditedInput
			if err = json.Unmarshal(edited, &ei); err == nil {
				a.EditedInput = &ei
			}
		}

		a.Error = errText
		out = append(out, a)
	}

	return out, rows.Err()
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *Store) encryptSecret(inboxID uuid.UUID, plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, nil
	}

	if s.cipher == nil {
		return nil, errors.New("cannot store a signing secret: no master encryption key configured")
	}

	blob, err := s.cipher.Encrypt(inboxID, plain)
	if err != nil {
		return nil, fmt.Errorf("cannot encrypt signing secret: %w", err)
	}

	return blob, nil
}

func (s *Store) decryptSecret(inboxID uuid.UUID, blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}

	if s.cipher == nil {
		// Without the key we cannot read the secret back. We refuse to guess: the inbox
		// simply behaves as if signing was not configured (and the API says so).
		return nil, nil
	}

	plain, err := s.cipher.Decrypt(inboxID, blob)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt signing secret: %w", err)
	}

	return plain, nil
}

func isUniqueViolation(err error, constraint string) bool {
	return err != nil && strings.Contains(err.Error(), constraint) &&
		strings.Contains(err.Error(), "duplicate key")
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}

	neg := v < 0
	if neg {
		v = -v
	}

	var buf [20]byte

	i := len(buf)

	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}

	if neg {
		i--
		buf[i] = '-'
	}

	return string(buf[i:])
}

// withAlias prefixes a comma separated column list with a table alias.
func withAlias(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i := range parts {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}

		p = strings.ReplaceAll(p, "\n\t", " ")
		parts[i] = alias + "." + p
	}

	return strings.Join(parts, ", ")
}
