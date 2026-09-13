package httpapi

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// EventListItemDTO is one row of the event list. It carries a short preview instead of the
// body: a 1 MiB payload must not be shipped for every row of a page.
type EventListItemDTO struct {
	ID             string     `json:"id"`
	InboxID        string     `json:"inbox_id"`
	Method         string     `json:"method"`
	Path           string     `json:"path"`
	Query          string     `json:"query"`
	ContentType    string     `json:"content_type"`
	BodySize       int        `json:"body_size"`
	Preview        string     `json:"preview"`
	ClientIP       string     `json:"client_ip"`
	SignatureValid *bool      `json:"signature_valid"`
	CreatedAt      time.Time  `json:"created_at"`
	ReplayCount    int        `json:"replay_count"`
	LastOutcome    string     `json:"last_outcome,omitempty"`
	LastStatus     *int       `json:"last_status_code,omitempty"`
	LastReplayedAt *time.Time `json:"last_replayed_at,omitempty"`
}

// EventDTO is the full event (detail view): the body is included as base64 so that binary
// payloads survive the round trip untouched.
type EventDTO struct {
	ID             string    `json:"id"`
	InboxID        string    `json:"inbox_id"`
	Method         string    `json:"method"`
	Path           string    `json:"path"`
	Query          string    `json:"query"`
	ContentType    string    `json:"content_type"`
	Headers        []HeaderDTO `json:"headers"`
	BodyBase64     string    `json:"body_base64"`
	BodySize       int       `json:"body_size"`
	ClientIP       string    `json:"client_ip"`
	SignatureValid *bool     `json:"signature_valid"`
	CreatedAt      time.Time `json:"created_at"`
}

// HeaderDTO is a header.
//
// `revealed` distinguishes the two ways a protected value can look identical to the UI:
// it is true only when the stored value was actually decrypted. A masked value shown while
// revealing is therefore "the original is gone", not "you are not allowed to see it" -
// and without this flag the client cannot tell them apart (Reveal() hands back the mask
// unchanged when there is nothing to decrypt).
type HeaderDTO struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
	Revealed  bool   `json:"revealed"`
}

// The list preview is produced by the storage layer (first 512 characters of the
// searchable text mirror). Binary payloads have no mirror, so their preview is empty -
// which is exactly what the UI should show instead of mojibake.

// GET /v1/inboxes/{id}/events
func (a API) listEvents(w http.ResponseWriter, r *http.Request) {
	inboxID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedInbox(w, r, inboxID); !ok {
		return
	}

	limit, offset := a.paging(r, 20)

	from, err := parseTime(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time", err.Error())

		return
	}

	to, err := parseTime(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time", err.Error())

		return
	}

	items, total, err := a.deps.Store.ListEvents(r.Context(), storage.EventFilter{
		InboxID:     inboxID,
		From:        from,
		To:          to,
		ContentType: r.URL.Query().Get("content_type"),
		Method:      strings.ToUpper(r.URL.Query().Get("method")),
		Query:       r.URL.Query().Get("q"),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		a.writeStoreError(w, err, "cannot list events")

		return
	}

	out := make([]EventListItemDTO, 0, len(items))

	for _, it := range items {
		out = append(out, EventListItemDTO{
			ID:             it.ID.String(),
			InboxID:        it.InboxID.String(),
			Method:         it.Method,
			Path:           it.Path,
			Query:          it.Query,
			ContentType:    it.ContentType,
			BodySize:       it.BodySize,
			Preview:        it.Preview,
			ClientIP:       it.ClientIP,
			SignatureValid: it.SignatureValid,
			CreatedAt:      it.CreatedAt,
			ReplayCount:    it.ReplayCount,
			LastOutcome:    it.LastOutcome,
			LastStatus:     it.LastStatus,
			LastReplayedAt: it.LastReplayedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total, "limit": limit, "offset": offset})
}

// DELETE /v1/inboxes/{id}/events - clears the inbox (events + their replay history).
func (a API) clearEvents(w http.ResponseWriter, r *http.Request) {
	inboxID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedInbox(w, r, inboxID); !ok {
		return
	}

	// One statement: replay attempts are removed by the foreign key (ON DELETE CASCADE).
	// The previous implementation listed and deleted one by one, which meant 500 round
	// trips for a full inbox.
	removed, err := a.deps.Store.DeleteInboxEvents(r.Context(), inboxID)
	if err != nil {
		a.writeStoreError(w, err, "cannot clear events")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": removed})
}

// GET /v1/events/{id}
func (a API) getEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	ev, ok := a.ownedEvent(w, r, id)
	if !ok {
		return
	}

	// Protected values are only decrypted when (a) the caller explicitly asks for it and
	// (b) access control is actually enabled. Without --auth-token the API is open, so
	// revealing secrets would defeat the whole point of masking them.
	reveal := a.accessControlEnabled() && truthy(r.URL.Query().Get("reveal"))

	if reveal {
		a.deps.Log.Info("sensitive headers revealed",
			zap.String("event_id", ev.ID.String()),
			zap.String("remote_addr", r.RemoteAddr))
	}

	headers := make([]HeaderDTO, 0, len(ev.Headers))

	for _, h := range ev.Headers {
		value := h.Value
		revealed := false

		if reveal {
			value = capture.Reveal(a.deps.Cipher, ev.InboxID, h)
			// Only a value that actually changed was decrypted; the mask surviving means
			// the original was never stored (or was stored before a key was configured).
			revealed = value != h.Value
		}

		headers = append(headers, HeaderDTO{
			Name:      h.Name,
			Value:     value,
			Sensitive: h.Sensitive,
			Revealed:  revealed,
		})
	}

	writeJSON(w, http.StatusOK, EventDTO{
		ID:             ev.ID.String(),
		InboxID:        ev.InboxID.String(),
		Method:         ev.Method,
		Path:           ev.Path,
		Query:          ev.Query,
		ContentType:    ev.ContentType,
		Headers:        headers,
		BodyBase64:     base64.StdEncoding.EncodeToString(ev.Body),
		BodySize:       ev.BodySize,
		ClientIP:       ev.ClientIP,
		SignatureValid: ev.SignatureValid,
		CreatedAt:      ev.CreatedAt,
	})
}

// DELETE /v1/events/{id}
func (a API) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedEvent(w, r, id); !ok {
		return
	}

	if err = a.deps.Store.DeleteEvent(r.Context(), id); err != nil {
		a.writeStoreError(w, err, "cannot delete event")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
