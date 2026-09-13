package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/replay"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// ReplayAttemptDTO mirrors storage.ReplayAttempt with string ids for the UI.
type ReplayAttemptDTO struct {
	ID               string  `json:"id"`
	EventID          string  `json:"event_id"`
	AttemptNo        int     `json:"attempt_no"`
	RetryOf          string  `json:"retry_of,omitempty"`
	TargetURL        string  `json:"target_url"`
	SignApplied      bool    `json:"sign_applied"`
	StartedAt        string  `json:"started_at"`
	FinishedAt       *string `json:"finished_at,omitempty"`
	DurationMS       int     `json:"duration_ms"`
	StatusCode       *int    `json:"status_code,omitempty"`
	ResponsePreview  string  `json:"response_preview,omitempty"`
	PreviewTruncated bool    `json:"preview_truncated"`
	Error            string  `json:"error,omitempty"`
	Outcome          string  `json:"outcome"`
	Edited           bool    `json:"edited"`
}

func replayDTO(a storage.ReplayAttempt) ReplayAttemptDTO {
	dto := ReplayAttemptDTO{
		ID:               a.ID.String(),
		EventID:          a.EventID.String(),
		AttemptNo:        a.AttemptNo,
		TargetURL:        a.TargetURL,
		SignApplied:      a.SignApplied,
		StartedAt:        a.StartedAt.Format("2006-01-02T15:04:05.000Z"),
		DurationMS:       a.DurationMS,
		StatusCode:       a.StatusCode,
		ResponsePreview:  a.ResponsePreview,
		PreviewTruncated: a.PreviewTruncated,
		Error:            a.Error,
		Outcome:          a.Outcome,
		Edited:           a.EditedInput != nil,
	}

	if a.RetryOf != nil {
		dto.RetryOf = a.RetryOf.String()
	}

	if a.FinishedAt != nil {
		finished := a.FinishedAt.Format("2006-01-02T15:04:05.000Z")
		dto.FinishedAt = &finished
	}

	return dto
}

// createReplayRequest accepts the edited body as base64.
//
// Why not a plain JSON string: encoding/json replaces invalid UTF-8 with U+FFFD when
// decoding into a string, which would silently corrupt binary payloads (and break the
// receiver's HMAC check). Base64 keeps the bytes intact.
type createReplayRequest struct {
	TargetURL  string `json:"target_url"`
	Method     string `json:"method,omitempty"`
	Body       string `json:"body,omitempty"`
	BodyBase64 string `json:"body_base64,omitempty"`
	Headers    []KV   `json:"headers,omitempty"`
	Sign       bool   `json:"sign,omitempty"`
}

// POST /v1/events/{id}/replay
//
// The response status reflects the *attempt*, not the target's status:
//
//	200 - attempts were executed (see outcome for success/http_error/timeout/...)
//	400 - the target URL is not usable
//	403 - the target is refused by the policy (still recorded as an attempt)
//	404 - unknown event
func (a API) createReplay(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	var req createReplayRequest
	if err = decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_json", "cannot parse request body")

		return
	}

	if strings.TrimSpace(req.TargetURL) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "target_url is required")

		return
	}

	ev, ok := a.ownedEvent(w, r, id)
	if !ok {
		return
	}

	inbox, ok := a.ownedInbox(w, r, ev.InboxID)
	if !ok {
		return
	}

	var edit *storage.EditedInput

	editedBody := req.Body

	if req.BodyBase64 != "" {
		raw, decErr := base64.StdEncoding.DecodeString(req.BodyBase64)
		if decErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "body_base64 must be base64 encoded")

			return
		}

		editedBody = string(raw)
	}

	if editedBody != "" || len(req.Headers) > 0 || req.Method != "" {
		edit = &storage.EditedInput{Method: req.Method, Body: editedBody}

		for _, h := range req.Headers {
			// Filter here as well as before sending: a header pasted into the editor must
			// not end up in the database in the clear.
			if !replay.IsForwardable(h.Name) {
				continue
			}

			edit.Headers = append(edit.Headers, storage.HttpHeader{Name: h.Name, Value: h.Value})
		}
	}

	attempts, err := a.deps.Replay.Run(r.Context(), replay.Request{
		Event:  *ev,
		Inbox:  *inbox,
		Target: req.TargetURL,
		Edit:   edit,
		Sign:   req.Sign,
	})

	switch {
	case errors.Is(err, replay.ErrBlockedTarget):
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":    map[string]any{"code": "target_blocked", "message": err.Error()},
			"attempts": dtoList(attempts),
		})

		return
	case errors.Is(err, replay.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, "invalid_target", err.Error())

		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())

		return
	}

	if len(attempts) == 0 {
		writeError(w, http.StatusInternalServerError, "internal_error", "no attempt was recorded")

		return
	}

	last := attempts[len(attempts)-1]

	// HTTP 200 means "the attempt was executed and recorded", not "the target accepted
	// it". `ok` makes that explicit for clients.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       last.Outcome == storage.OutcomeSuccess,
		"attempts": dtoList(attempts),
		"last":     replayDTO(last),
	})
}

func dtoList(in []storage.ReplayAttempt) []ReplayAttemptDTO {
	out := make([]ReplayAttemptDTO, 0, len(in))
	for _, a := range in {
		out = append(out, replayDTO(a))
	}

	return out
}

// GET /v1/events/{id}/replays.
func (a API) listReplays(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if _, ok := a.ownedEvent(w, r, id); !ok {
		return
	}

	items, err := a.deps.Store.ListReplays(r.Context(), id, 100)
	if err != nil {
		a.writeStoreError(w, err, "cannot list replays")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": dtoList(items)})
}

// GET /v1/events/{id}/export?format=curl|json
//
// Producing a ready to paste curl command is the single most useful "export" for this
// product: it lets a developer take a captured request straight into a terminal.
func (a API) exportEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	ev, ok := a.ownedEvent(w, r, id)
	if !ok {
		return
	}

	// Same rule as the detail endpoint: no reveal without access control.
	reveal := a.accessControlEnabled() && truthy(r.URL.Query().Get("reveal"))

	if r.URL.Query().Get("format") == "curl" {
		var sb strings.Builder

		// A captured request only stores the path, so without a configured root the
		// exported command cannot be pasted as-is. Say so instead of pretending.
		if root := a.deps.Settings.PublicURLRoot; root != "" {
			sb.WriteString("# 已使用服务端配置的对外根地址\n")
		} else {
			sb.WriteString("# 服务端未配置 --public-url-root，请把下面的地址换成完整地址后执行\n")
		}

		sb.WriteString("curl -i -X ")
		sb.WriteString(ev.Method)
		sb.WriteString(" '")
		sb.WriteString(a.deps.Settings.PublicURLRoot)
		sb.WriteString(ev.Path)

		if ev.Query != "" {
			sb.WriteString("?")
			sb.WriteString(ev.Query)
		}

		sb.WriteString("'")

		for _, h := range ev.Headers {
			// Content-Length is recomputed by curl from --data-binary; keeping the
			// captured value makes the exported command fail whenever it disagrees.
			if strings.EqualFold(h.Name, "Content-Length") {
				continue
			}

			value := h.Value
			if reveal {
				value = capture.Reveal(a.deps.Cipher, ev.InboxID, h)
			}

			sb.WriteString(" \\\n  -H '")
			sb.WriteString(h.Name)
			sb.WriteString(": ")
			sb.WriteString(strings.ReplaceAll(value, "'", `'\''`))
			sb.WriteString("'")
		}

		if len(ev.Body) > 0 {
			// A heredoc breaks if the payload itself contains the delimiter on its own
			// line, so pick one that cannot appear in this body.
			delim := "EOF"
			for strings.Contains(string(ev.Body), "\n"+delim+"\n") {
				delim += "_"
			}

			sb.WriteString(" \\\n  --data-binary @- <<'" + delim + "'\n")
			sb.Write(ev.Body)
			sb.WriteString("\n" + delim)
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(sb.String()))

		return
	}

	// The JSON export honours reveal exactly like the cURL one: an export that always
	// ships masked secrets would be useless as a reproduction case.
	headers := make([]map[string]any, 0, len(ev.Headers))

	for _, h := range ev.Headers {
		value := h.Value
		if reveal {
			value = capture.Reveal(a.deps.Cipher, ev.InboxID, h)
		}

		headers = append(headers, map[string]any{
			"name":      h.Name,
			"value":     value,
			"sensitive": h.Sensitive,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          ev.ID.String(),
		"method":      ev.Method,
		"path":        ev.Path,
		"query":       ev.Query,
		"headers":     headers,
		"body_base64": base64.StdEncoding.EncodeToString(ev.Body),
		"created_at":  ev.CreatedAt,
	})
}
