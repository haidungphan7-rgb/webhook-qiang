package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// hopByHop and identity headers that must never be replayed: they describe the *original*
// connection, not the new one. The sensitive set (Authorization, Cookie, X-API-Key, ...)
// is handled by capture.IsSensitive and is stripped for confidentiality.
var neverForward = map[string]bool{
	"host":                true,
	"content-length":      true,
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"accept-encoding":     true, // we control the transport encoding
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-real-ip":           true,
	"cf-connecting-ip":    true,
	"x-wh-event-id":       true,
}

// Service executes replays and records every attempt.
type Service struct {
	log    *zap.Logger
	store  storage.Store
	policy Policy
	client *http.Client // built once: the transport owns an idle connection pool
}

// New creates the replay service.
func New(log *zap.Logger, store storage.Store, policy Policy) *Service {
	return &Service{log: log, store: store, policy: policy, client: policy.Client()}
}

// Request describes one replay invocation.
type Request struct {
	Event  storage.Event
	Inbox  storage.Inbox
	Target string
	// Edit carries user modifications. Nil means "send the original request untouched".
	Edit *storage.EditedInput
	// Sign recomputes the HMAC signature with the inbox secret before sending.
	Sign bool
}

// Run performs the replay (including retries) and returns every attempt, oldest first.
//
// Every attempt is persisted - including the ones refused by the policy. That is
// deliberate: "the target limit must be verifiable" is much easier to demonstrate when
// the refusal itself shows up in the history.
func (s *Service) Run(ctx context.Context, req Request) ([]storage.ReplayAttempt, error) {
	if err := s.policy.ValidateTarget(ctx, req.Target); err != nil {
		attempt := s.blockedAttempt(req, err)

		if storeErr := s.store.CreateReplay(ctx, &attempt); storeErr != nil {
			s.log.Error("cannot record blocked replay", zap.Error(storeErr))
		}

		return []storage.ReplayAttempt{attempt}, err
	}

	client := s.client

	var (
		attempts []storage.ReplayAttempt
		firstID  uuid.UUID
	)

	total := 1 + s.policy.MaxRetries

	for i := 1; i <= total; i++ {
		if i > 1 {
			s.sleep(backoff(s.policy.Backoff, i-1), ctx)
		}

		attempt := s.execute(ctx, client, req, i)

		if firstID != uuid.Nil {
			id := firstID
			attempt.RetryOf = &id
		}

		if err := s.store.CreateReplay(ctx, &attempt); err != nil {
			s.log.Error("cannot record replay attempt", zap.Error(err))

			// Every attempt has to be recorded - that is what makes the target policy
			// auditable. If the write fails we cannot retry either (the next attempt would
			// have no parent), so the caller gets an error instead of a "success" whose
			// history was never saved.
			return attempts, fmt.Errorf("cannot record replay attempt %d: %w", i, err)
		}

		if firstID == uuid.Nil {
			firstID = attempt.ID
		}

		attempts = append(attempts, attempt)

		if !retryable(attempt.Outcome, attempt.StatusCode) {
			break
		}
	}

	return attempts, nil
}

func (s *Service) blockedAttempt(req Request, err error) storage.ReplayAttempt {
	now := time.Now().UTC()

	return storage.ReplayAttempt{
		ID:         uuid.New(),
		EventID:    req.Event.ID,
		InboxID:    req.Inbox.ID,
		AttemptNo:  1,
		TargetURL:  req.Target,
		StartedAt:  now,
		FinishedAt: func() *time.Time { return &now }(),
		Outcome:    storage.OutcomeBlocked,
		Error:      err.Error(),
		CreatedAt:  now,
	}
}

func (s *Service) execute(
	ctx context.Context,
	client *http.Client,
	req Request,
	attemptNo int,
) storage.ReplayAttempt {
	started := time.Now().UTC()

	attempt := storage.ReplayAttempt{
		ID:          uuid.New(),
		EventID:     req.Event.ID,
		InboxID:     req.Inbox.ID,
		AttemptNo:   attemptNo,
		TargetURL:   req.Target,
		StartedAt:   started,
		CreatedAt:   started,
		SignApplied: req.Sign && len(req.Inbox.SigningSecret) > 0,
	}

	if req.Edit != nil {
		attempt.EditedInput = req.Edit
	}

	method := req.Event.Method
	body := req.Event.Body

	if req.Edit != nil {
		if req.Edit.Method != "" {
			method = strings.ToUpper(req.Edit.Method)
		}

		if req.Edit.Body != "" {
			body = []byte(req.Edit.Body)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.Target, bytes.NewReader(body))
	if err != nil {
		return finish(&attempt, time.Now(), fmt.Errorf("%w: %s", ErrInvalidURL, err.Error()))
	}

	httpReq.Header = req.headers(body)

	resp, err := client.Do(httpReq)
	if err != nil {
		return finish(&attempt, time.Now(), err)
	}

	defer func() { _ = resp.Body.Close() }()

	attempt.StatusCode = &resp.StatusCode

	preview, truncated, readErr := readPreview(resp.Body, s.policy.MaxPreview)
	attempt.ResponsePreview = preview
	attempt.PreviewTruncated = truncated

	// A status code without a readable body is not a success: the whole point of a replay
	// is to see what came back, and recording "success" here would hide a broken target.
	if readErr != nil {
		return finish(&attempt, time.Now(), fmt.Errorf("cannot read response body: %w", readErr))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		attempt.Outcome = storage.OutcomeSuccess
	} else {
		attempt.Outcome = storage.OutcomeHTTPError
	}

	return finish(&attempt, time.Now(), nil)
}

// headers copies the captured headers, dropping everything we must not forward, and
// optionally recomputes the signature over the (possibly edited) body.
//
// Implemented on Request (not as a free function) so the rule set can be unit tested
// without spinning up a service or a database.
// IsForwardable reports whether a header may be sent on a replay. It is exported so the
// API layer filters user supplied headers with exactly the same rule set - otherwise
// "filtered before sending" and "filtered before storing" would drift apart.
func IsForwardable(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))

	return !neverForward[lower] && !capture.IsSensitive(lower)
}

func (r Request) headers(body []byte) http.Header {
	out := http.Header{}

	if r.Edit != nil && len(r.Edit.Headers) > 0 {
		for _, h := range r.Edit.Headers {
			if !IsForwardable(h.Name) {
				continue
			}

			out.Set(h.Name, h.Value)
		}
	} else {
		for _, h := range r.Event.Headers {
			if !IsForwardable(h.Name) {
				continue
			}

			out.Set(h.Name, h.Value)
		}
	}

	// The captured Content-Type is part of the request, not of the connection, so it is
	// always forwarded (unless the user edited it).
	if ct := r.Event.ContentType; ct != "" && out.Get("Content-Type") == "" {
		out.Set("Content-Type", ct)
	}

	if r.Sign && len(r.Inbox.SigningSecret) > 0 && r.Inbox.SignatureHeader != "" {
		mac := crypto.Sign(r.Inbox.SigningSecret, body)

		switch r.Inbox.SignatureScheme {
		case crypto.SchemeSha256Prefixed:
			out.Set(r.Inbox.SignatureHeader, "sha256="+mac)
		default:
			out.Set(r.Inbox.SignatureHeader, mac)
		}
	}

	return out
}

// finish closes the attempt: sets the duration, the outcome (when not set) and a
// human readable error.
func finish(a *storage.ReplayAttempt, ended time.Time, err error) storage.ReplayAttempt {
	a.FinishedAt = &ended
	a.DurationMS = int(ended.Sub(a.StartedAt).Milliseconds())

	switch {
	case err != nil:
		a.Error = err.Error()
		a.Outcome = classifyError(err)
	case a.Outcome == "":
		a.Outcome = storage.OutcomeNetworkError
		a.Error = "empty outcome"
	}

	return *a
}

func classifyError(err error) string {
	if err == nil {
		return storage.OutcomeSuccess
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// Match on the wrapped sentinel, never on the message text: the UI relies on
		// "blocked" being distinguishable from "network error" (the task requires the
		// target restriction to be verifiable).
		if errors.Is(urlErr.Err, ErrBlockedTarget) {
			return storage.OutcomeBlocked
		}

		if urlErr.Timeout() || errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return storage.OutcomeTimeout
		}

		return storage.OutcomeNetworkError
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return storage.OutcomeTimeout
	}

	return storage.OutcomeNetworkError
}

// readPreview keeps at most max bytes and reports whether the body was cut.
//
// The error is returned instead of being swallowed: a truncated or aborted transfer is
// information the caller needs, not noise (see the outcome rules in the service).
func readPreview(rc io.Reader, max int) (string, bool, error) {
	if max <= 0 {
		max = 4096
	}

	raw, err := io.ReadAll(io.LimitReader(rc, int64(max)+1))
	if err != nil {
		// Keep whatever arrived before the failure, but flag it as incomplete.
		if len(raw) > max {
			raw = raw[:max]
		}

		return string(raw), true, err
	}

	if len(raw) > max {
		return string(raw[:max]), true, nil
	}

	return string(raw), false, nil
}

// retryable reports whether a failed attempt deserves another try.
func retryable(outcome string, status *int) bool {
	switch outcome {
	case storage.OutcomeTimeout, storage.OutcomeNetworkError:
		return true
	case storage.OutcomeHTTPError:
		return status != nil && *status >= 500
	default:
		return false
	}
}

// backoff returns an exponential delay with ±20% jitter (avoids retry storms).
func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
	}

	jitter := time.Duration(rand.Int63n(int64(d/5)+1)) - d/10 //nolint:gosec // jitter, not security

	return d + jitter
}

func (s *Service) sleep(d time.Duration, ctx context.Context) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
