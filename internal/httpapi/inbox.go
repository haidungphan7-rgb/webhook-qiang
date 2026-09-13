package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// InboxDTO is the API representation of an inbox.
type InboxDTO struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Token        string     `json:"token"`
	Enabled      bool       `json:"enabled"`
	ReceiveURL   string     `json:"receive_url"`
	EventCount   int        `json:"event_count"`
	LastEventAt  *time.Time `json:"last_event_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ResponseCode int        `json:"response_code"`
	ResponseDelayMS int     `json:"response_delay_ms"`
	ResponseBody string     `json:"response_body_base64,omitempty"`
	ResponseHeaders []KV    `json:"response_headers"`

	SignatureHeader  string `json:"signature_header"`
	SignatureScheme  string `json:"signature_scheme"`
	RequireSignature bool   `json:"require_signature"`
	HasSigningSecret bool   `json:"has_signing_secret"`

	RetentionMaxEvents int `json:"retention_max_events"`
	RetentionMaxDays   int `json:"retention_max_days"`
}

// KV is a generic key/value pair (headers).
type KV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (a API) inboxDTO(r *http.Request, in storage.Inbox) InboxDTO {
	dto := InboxDTO{
		ID:                in.ID.String(),
		Name:              in.Name,
		Token:             in.Token,
		Enabled:           in.Enabled,
		ReceiveURL:        a.receiveURL(r, in.Token),
		EventCount:        in.EventCount,
		LastEventAt:       in.LastEventAt,
		CreatedAt:         in.CreatedAt,
		UpdatedAt:         in.UpdatedAt,
		ResponseCode:      in.ResponseCode,
		ResponseDelayMS:   in.ResponseDelayMS,
		ResponseBody:      base64.StdEncoding.EncodeToString(in.ResponseBody),
		ResponseHeaders:   headersKV(in.ResponseHeaders),
		SignatureHeader:   in.SignatureHeader,
		SignatureScheme:   in.SignatureScheme,
		RequireSignature:  in.RequireSignature,
		HasSigningSecret:  len(in.SigningSecret) > 0,
		RetentionMaxEvents: in.RetentionMaxEvents,
		RetentionMaxDays:   in.RetentionMaxDays,
	}

	return dto
}

func headersKV(h []storage.HttpHeader) []KV {
	out := make([]KV, 0, len(h))
	for _, v := range h {
		out = append(out, KV{Name: v.Name, Value: v.Value})
	}

	return out
}

func (a API) receiveURL(r *http.Request, token string) string {
	base := a.deps.Settings.PublicURLRoot
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}

		base = scheme + "://" + r.Host
	}

	return strings.TrimRight(base, "/") + "/hooks/" + token
}

// GET /v1/inboxes
func (a API) listInboxes(w http.ResponseWriter, r *http.Request) {
	limit, offset := a.paging(r, 20)

	var enabled *bool
	if v := r.URL.Query().Get("enabled"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			enabled = &b
		}
	}

	items, total, err := a.deps.Store.ListInboxes(r.Context(), storage.InboxFilter{
		OwnerKey: a.ownerKey(r),
		Query:    r.URL.Query().Get("q"),
		Enabled:  enabled,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		a.writeStoreError(w, err, "cannot list inboxes")

		return
	}

	out := make([]InboxDTO, 0, len(items))
	for _, in := range items {
		out = append(out, a.inboxDTO(r, in))
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total, "limit": limit, "offset": offset})
}

type createInboxRequest struct {
	Name string `json:"name"`
}

// POST /v1/inboxes
func (a API) createInbox(w http.ResponseWriter, r *http.Request) {
	var req createInboxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_json", "cannot parse request body")

		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "name is required")

		return
	}

	if len(req.Name) > 120 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "name is too long (max 120)")

		return
	}

	token, err := storage.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate token")

		return
	}

	in := storage.Inbox{
		OwnerKey:           a.ownerKey(r),
		Name:               req.Name,
		Token:              token,
		Enabled:            true,
		ResponseCode:       200,
		SignatureHeader:    "X-Signature",
		SignatureScheme:    crypto.SchemeHex,
		RetentionMaxEvents: a.deps.Settings.RetentionMaxEvents,
		RetentionMaxDays:   a.deps.Settings.RetentionMaxDays,
	}

	// Retry a handful of times on the (astronomically unlikely) token collision instead
	// of surfacing a confusing 409 to the user.
	for attempt := 0; attempt < 5; attempt++ {
		err = a.deps.Store.CreateInbox(r.Context(), &in)
		if err == nil {
			break
		}

		if !errors.Is(err, storage.ErrTokenTaken) {
			a.writeStoreError(w, err, "cannot create inbox")

			return
		}

		if token, err = storage.NewToken(); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate token")

			return
		}

		in.Token = token
	}

	if err != nil {
		writeError(w, http.StatusConflict, "token_taken", "cannot allocate a unique token")

		return
	}

	writeJSON(w, http.StatusCreated, a.inboxDTO(r, in))
}

// GET /v1/inboxes/{id}
func (a API) getInbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	in, ok := a.ownedInbox(w, r, id)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, a.inboxDTO(r, *in))
}

type patchInboxRequest struct {
	Name              *string `json:"name"`
	Enabled           *bool   `json:"enabled"`
	ResponseCode      *int    `json:"response_code"`
	ResponseDelayMS   *int    `json:"response_delay_ms"`
	ResponseBody      *string `json:"response_body_base64"`
	SignatureHeader   *string `json:"signature_header"`
	SignatureScheme   *string `json:"signature_scheme"`
	RequireSignature  *bool   `json:"require_signature"`
	SigningSecret     *string `json:"signing_secret"`
	RetentionMaxEvents *int   `json:"retention_max_events"`
	RetentionMaxDays   *int   `json:"retention_max_days"`
}

// PATCH /v1/inboxes/{id}
func (a API) patchInbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	var req patchInboxRequest
	if err = decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_json", "cannot parse request body")

		return
	}

	if req.ResponseCode != nil {
		code := *req.ResponseCode

		// 1xx is a protocol continuation and 204/304 are defined to have no body, so
		// none of them can carry the event id back to the caller - the task requires the
		// capture response to include it.
		if code < 200 || code > 599 || code == 204 || code == 304 {
			writeError(w, http.StatusUnprocessableEntity, "validation_error",
				"response_code must be between 200 and 599 and cannot be 204 or 304 (no response body to carry the event id)")

			return
		}
	}

	if req.SignatureScheme != nil {
		switch *req.SignatureScheme {
		case crypto.SchemeHex, crypto.SchemeSha256Prefixed:
		default:
			writeError(w, http.StatusUnprocessableEntity, "validation_error",
				"signature_scheme must be one of: "+crypto.SchemeHex+", "+crypto.SchemeSha256Prefixed)

			return
		}
	}

	if req.ResponseDelayMS != nil && (*req.ResponseDelayMS < 0 || *req.ResponseDelayMS > 30_000) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "response_delay_ms must be between 0 and 30000")

		return
	}

	patch := storage.InboxPatch{
		Name:               req.Name,
		Enabled:            req.Enabled,
		SignatureHeader:    req.SignatureHeader,
		SignatureScheme:    req.SignatureScheme,
		RequireSignature:   req.RequireSignature,
		RetentionMaxEvents: req.RetentionMaxEvents,
		RetentionMaxDays:   req.RetentionMaxDays,
		ResponseCode:       req.ResponseCode,
		ResponseDelayMS:    req.ResponseDelayMS,
	}

	if req.ResponseBody != nil {
		raw, decErr := base64.StdEncoding.DecodeString(*req.ResponseBody)
		if decErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "response_body_base64 must be base64")

			return
		}

		patch.ResponseBody = &raw
	}

	if req.SigningSecret != nil {
		secret := []byte(strings.TrimSpace(*req.SigningSecret))

		if len(secret) == 0 {
			secret = nil
		} else if a.deps.Cipher == nil {
			writeError(w, http.StatusConflict, "encryption_required",
				"a signing secret can only be stored when an encryption key is configured")

			return
		}

		patch.SigningSecret = &secret
	}

	if _, ok := a.ownedInbox(w, r, id); !ok {
		return
	}

	if err = a.deps.Store.UpdateInbox(r.Context(), id, patch); err != nil {
		a.writeStoreError(w, err, "cannot update inbox")

		return
	}

	in, err := a.deps.Store.GetInbox(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err, "cannot read inbox")

		return
	}

	writeJSON(w, http.StatusOK, a.inboxDTO(r, *in))
}

// DELETE /v1/inboxes/{id}
func (a API) deleteInbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedInbox(w, r, id); !ok {
		return
	}

	if err = a.deps.Store.DeleteInbox(r.Context(), id); err != nil {
		a.writeStoreError(w, err, "cannot delete inbox")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/inboxes/{id}/token/rotate
func (a API) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedInbox(w, r, id); !ok {
		return
	}

	token, err := a.deps.Store.RotateToken(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err, "cannot rotate token")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": token, "receive_url": a.receiveURL(r, token)})
}
