// Package notify defines the realtime message pushed to the UI when a webhook arrives.
//
// It carries metadata only - never the body. Bodies can be up to 1 MiB and a busy inbox
// would otherwise push megabytes over the websocket; the UI fetches the body lazily when
// the user actually opens an event.
package notify

import (
	"github.com/google/uuid"
)

// Action describes what happened.
type Action string

const (
	// ActionCreate is published after an inbound request has been stored.
	ActionCreate Action = "create"
	// ActionDelete is published when a single event is deleted.
	ActionDelete Action = "delete"
	// ActionClear is published when all events of an inbox are deleted.
	ActionClear Action = "clear"
)

// Event is the payload of a notification.
type Event struct {
	ID          uuid.UUID `json:"id"`
	InboxID     uuid.UUID `json:"inbox_id"`
	Method      string    `json:"method"`
	ContentType string    `json:"content_type"`
	BodySize    int       `json:"body_size"`
	ClientIP    string    `json:"client_ip"`
	CreatedAtMS int64     `json:"created_at_ms"`
}

// Message is what travels over the pub/sub and the websocket.
type Message struct {
	Action Action `json:"action"`
	Event  Event  `json:"event"`
}
