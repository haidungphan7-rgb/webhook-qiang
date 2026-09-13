package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// upgrader is intentionally permissive about the origin: this is a developer tool that is
// typically served from a different port (vite dev server) or from a LAN address.
var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	CheckOrigin:      sameOrigin,
}

// sameOrigin rejects cross site websocket upgrades. Without it any page in the browser
// could open a socket and read the event stream of an inbox it does not own.
//
// Non browser clients (no Origin header) are still allowed - the API is meant to be
// scriptable - they are protected by --auth-token instead.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(u.Host, r.Host)
}

// GET /v1/inboxes/{id}/events/subscribe - websocket stream of new events.
//
// The socket carries metadata only (no bodies): a busy inbox would otherwise stream
// megabytes. The UI reacts to the notification and reloads the page of events it is
// currently showing, which also keeps paging and filtering consistent.
func (a API) subscribe(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a uuid")

		return
	}

	if a.deps.PubSub == nil {
		writeError(w, http.StatusNotImplemented, "realtime_disabled", "realtime updates are not available")

		return
	}

	// The event stream belongs to the inbox owner.
	if _, ok := a.ownedInbox(w, r, id); !ok {
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		a.deps.Log.Warn("cannot upgrade to websocket", zap.Error(err))

		return
	}

	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err = a.stream(ctx, cancel, conn, id); err != nil {
		a.deps.Log.Debug("subscription ended", zap.Error(err))
	}
}

func (a API) stream(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, inboxID uuid.UUID) error {
	events, unsubscribe, err := a.deps.PubSub.Subscribe(ctx, inboxID.String())
	if err != nil {
		return err
	}

	defer unsubscribe()

	// A websocket that never receives anything from the peer can stay half-open for a very
	// long time. The ping/pong pair detects that and lets us tear the subscription down.
	const (
		pingInterval = 25 * time.Second
		pongWait     = 60 * time.Second
	)

	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()

	// Read loop: we do not expect messages, but a close frame (or a dead peer) is only
	// detected by reading.
	go func() {
		defer cancel()

		for {
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-events:
			if !ok {
				return nil
			}

			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

			if err = conn.WriteJSON(msg); err != nil {
				return err
			}
		}
	}
}
