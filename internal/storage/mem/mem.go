// Package mem is an in-memory implementation of storage.Store.
//
// It exists so the HTTP layer can be tested without a database: handlers, the capture
// middleware and the replay service are all exercised against this driver in the unit
// tests. It is intentionally NOT wired into the CLI - PostgreSQL is the only production
// driver, and having a second implementation also proves the Store interface is not
// accidentally PostgreSQL shaped.
package mem

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// Store is an in-memory storage.Store.
type Store struct {
	mu      sync.RWMutex
	inboxes map[uuid.UUID]storage.Inbox
	events  map[uuid.UUID]storage.Event
	replays map[uuid.UUID][]storage.ReplayAttempt
}

var _ storage.Store = (*Store)(nil)

// New creates an empty in-memory store.
func New() *Store {
	return &Store{
		inboxes: make(map[uuid.UUID]storage.Inbox),
		events:  make(map[uuid.UUID]storage.Event),
		replays: make(map[uuid.UUID][]storage.ReplayAttempt),
	}
}

func (s *Store) Ping(context.Context) error { return nil }

func (s *Store) Close() error { return nil }

// ── inboxes ─────────────────────────────────────────────────────────────────

func (s *Store) CreateInbox(_ context.Context, in *storage.Inbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}

	for _, existing := range s.inboxes {
		if existing.Token == in.Token {
			return storage.ErrTokenTaken
		}
	}

	now := time.Now().UTC()
	in.CreatedAt, in.UpdatedAt = now, now

	s.inboxes[in.ID] = *in

	return nil
}

func (s *Store) GetInbox(_ context.Context, id uuid.UUID) (*storage.Inbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	in, ok := s.inboxes[id]
	if !ok {
		return nil, storage.ErrInboxNotFound
	}

	return &in, nil
}

func (s *Store) GetInboxByToken(_ context.Context, token string) (*storage.Inbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !storage.IsValidToken(token) {
		return nil, storage.ErrInboxNotFound
	}

	for _, in := range s.inboxes {
		if in.Token == token {
			return &in, nil
		}
	}

	return nil, storage.ErrInboxNotFound
}

func (s *Store) ListInboxes(_ context.Context, f storage.InboxFilter) ([]storage.Inbox, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []storage.Inbox

	for _, in := range s.inboxes {
		if !f.AllOwners && in.OwnerKey != f.OwnerKey {
			continue
		}

		if f.Query != "" && !strings.Contains(strings.ToLower(in.Name), strings.ToLower(f.Query)) {
			continue
		}

		if f.Enabled != nil && in.Enabled != *f.Enabled {
			continue
		}

		count := 0

		var last *time.Time

		for _, ev := range s.events {
			if ev.InboxID != in.ID {
				continue
			}

			count++

			if last == nil || ev.CreatedAt.After(*last) {
				at := ev.CreatedAt
				last = &at
			}
		}

		in.EventCount = count
		in.LastEventAt = last

		out = append(out, in)
	}

	// newest first, id as tiebreaker (mirrors ORDER BY created_at DESC, id DESC)
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) ||
				(out[j].CreatedAt.Equal(out[i].CreatedAt) && out[j].ID.String() > out[i].ID.String()) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}

	total := len(out)

	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 20
	}

	if offset < 0 {
		offset = 0
	}

	if offset > total {
		offset = total
	}

	end := offset + limit
	if end > total {
		end = total
	}

	return out[offset:end], total, nil
}

func (s *Store) UpdateInbox(_ context.Context, id uuid.UUID, p storage.InboxPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	in, ok := s.inboxes[id]
	if !ok {
		return storage.ErrInboxNotFound
	}

	if p.Name != nil {
		in.Name = *p.Name
	}

	if p.Enabled != nil {
		in.Enabled = *p.Enabled
	}

	if p.SigningSecret != nil {
		in.SigningSecret = *p.SigningSecret
	}

	if p.SignatureHeader != nil {
		in.SignatureHeader = *p.SignatureHeader
	}

	if p.SignatureScheme != nil {
		in.SignatureScheme = *p.SignatureScheme
	}

	if p.RequireSignature != nil {
		in.RequireSignature = *p.RequireSignature
	}

	if p.RetentionMaxEvents != nil {
		in.RetentionMaxEvents = *p.RetentionMaxEvents
	}

	if p.RetentionMaxDays != nil {
		in.RetentionMaxDays = *p.RetentionMaxDays
	}

	// The custom response is part of the patch contract too: leaving these out would make
	// the memory driver behave differently from PostgreSQL and hide bugs behind a green
	// handler test.
	if p.ResponseCode != nil {
		in.ResponseCode = *p.ResponseCode
	}

	if p.ResponseDelayMS != nil {
		in.ResponseDelayMS = *p.ResponseDelayMS
	}

	if p.ResponseBody != nil {
		in.ResponseBody = *p.ResponseBody
	}

	in.UpdatedAt = time.Now().UTC()

	s.inboxes[id] = in

	return nil
}

func (s *Store) RotateToken(_ context.Context, id uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	in, ok := s.inboxes[id]
	if !ok {
		return "", storage.ErrInboxNotFound
	}

	token, err := storage.NewToken()
	if err != nil {
		return "", err
	}

	in.Token = token
	in.UpdatedAt = time.Now().UTC()

	s.inboxes[id] = in

	return token, nil
}

func (s *Store) DeleteInbox(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.inboxes[id]; !ok {
		return storage.ErrInboxNotFound
	}

	delete(s.inboxes, id)

	for eid, ev := range s.events {
		if ev.InboxID == id {
			delete(s.events, eid)
			delete(s.replays, eid)
		}
	}

	return nil
}

// ── events ──────────────────────────────────────────────────────────────────

func (s *Store) CreateEvent(_ context.Context, e *storage.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.inboxes[e.InboxID]; !ok {
		return storage.ErrInboxNotFound
	}

	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}

	e.BodySize = len(e.Body)

	// Same rule as the PostgreSQL driver - see storage.SearchableText. Keeping the two
	// drivers identical is what makes the unit tests meaningful: a binary body must be
	// non searchable here too, otherwise a regression would only show up in production.
	e.BodyText = storage.SearchableText(e.Body)

	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}

	s.events[e.ID] = *e

	return nil
}

func (s *Store) GetEvent(_ context.Context, id uuid.UUID) (*storage.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ev, ok := s.events[id]
	if !ok {
		return nil, storage.ErrEventNotFound
	}

	return &ev, nil
}

func (s *Store) ListEvents(_ context.Context, f storage.EventFilter) ([]storage.EventListItem, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []storage.Event

	for _, ev := range s.events {
		if ev.InboxID != f.InboxID {
			continue
		}

		if f.From != nil && ev.CreatedAt.Before(*f.From) {
			continue
		}

		if f.To != nil && ev.CreatedAt.After(*f.To) {
			continue
		}

		if f.ContentType != "" && mediaType(ev.ContentType) != mediaType(f.ContentType) {
			continue
		}

		if f.Method != "" && !strings.EqualFold(ev.Method, f.Method) {
			continue
		}

		// Search the text mirror only (no fallback to the raw body): this mirrors the SQL
		// driver exactly, so binary payloads are not searchable here either.
		if f.Query != "" && !strings.Contains(strings.ToLower(ev.BodyText), strings.ToLower(f.Query)) {
			continue
		}

		matched = append(matched, ev)
	}

	// newest first, id as tiebreaker (mirrors the SQL ordering)
	for i := 0; i < len(matched)-1; i++ {
		for j := i + 1; j < len(matched); j++ {
			if matched[j].CreatedAt.After(matched[i].CreatedAt) ||
				(matched[j].CreatedAt.Equal(matched[i].CreatedAt) && matched[j].ID.String() > matched[i].ID.String()) {
				matched[i], matched[j] = matched[j], matched[i]
			}
		}
	}

	total := len(matched)

	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 20
	}

	if offset < 0 {
		offset = 0
	}

	if offset > total {
		offset = total
	}

	end := offset + limit
	if end > total {
		end = total
	}

	out := make([]storage.EventListItem, 0, end-offset)

	for _, ev := range matched[offset:end] {
		item := storage.EventListItem{Event: ev}

		// The list never ships the body: a page of 100 events would transfer up to 100 MiB.
		if len(ev.BodyText) > 512 {
			item.Preview = ev.BodyText[:512]
		} else {
			item.Preview = ev.BodyText
		}

		attempts := s.replays[ev.ID]
		item.ReplayCount = len(attempts)

		if len(attempts) > 0 {
			// The SQL driver takes the newest by created_at; do the same instead of
			// relying on insertion order.
			last := attempts[0]

			for _, a := range attempts[1:] {
				if a.CreatedAt.After(last.CreatedAt) {
					last = a
				}
			}

			item.LastOutcome = last.Outcome
			item.LastStatus = last.StatusCode

			at := last.CreatedAt
			item.LastReplayedAt = &at
		}

		out = append(out, item)
	}

	return out, total, nil
}

func (s *Store) DeleteEvent(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.events[id]; !ok {
		return storage.ErrEventNotFound
	}

	delete(s.events, id)
	delete(s.replays, id)

	return nil
}

func (s *Store) DeleteInboxEvents(_ context.Context, inboxID uuid.UUID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int64

	for id, ev := range s.events {
		if ev.InboxID != inboxID {
			continue
		}

		delete(s.events, id)
		delete(s.replays, id)

		deleted++
	}

	return deleted, nil
}

func (s *Store) PruneEvents(_ context.Context, inboxID uuid.UUID, maxEvents, maxDays int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		deleted int64
		kept    []storage.Event
	)

	for _, ev := range s.events {
		if ev.InboxID != inboxID {
			continue
		}

		kept = append(kept, ev)
	}

	if maxDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -maxDays)

		var fresh []storage.Event

		for _, ev := range kept {
			if ev.CreatedAt.Before(cutoff) {
				delete(s.events, ev.ID)
				delete(s.replays, ev.ID)

				deleted++

				continue
			}

			fresh = append(fresh, ev)
		}

		kept = fresh
	}

	if maxEvents > 0 && len(kept) > maxEvents {
		// kept is not sorted yet; sort the same way as ListEvents before trimming.
		for i := 0; i < len(kept)-1; i++ {
			for j := i + 1; j < len(kept); j++ {
				if kept[j].CreatedAt.After(kept[i].CreatedAt) ||
					(kept[j].CreatedAt.Equal(kept[i].CreatedAt) && kept[j].ID.String() > kept[i].ID.String()) {
					kept[i], kept[j] = kept[j], kept[i]
				}
			}
		}

		for _, ev := range kept[maxEvents:] {
			delete(s.events, ev.ID)
			delete(s.replays, ev.ID)

			deleted++
		}
	}

	return deleted, nil
}

// ── replays ─────────────────────────────────────────────────────────────────

func (s *Store) CreateReplay(_ context.Context, a *storage.ReplayAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}

	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}

	if a.AttemptNo == 0 {
		a.AttemptNo = 1
	}

	s.replays[a.EventID] = append(s.replays[a.EventID], *a)

	return nil
}

func (s *Store) ListReplays(_ context.Context, eventID uuid.UUID, limit int) ([]storage.ReplayAttempt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.replays[eventID]

	out := make([]storage.ReplayAttempt, len(all))
	copy(out, all)

	// newest first
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}

	return strings.ToLower(strings.TrimSpace(ct))
}
