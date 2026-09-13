package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

type harness struct {
	deps  Deps
	store *mem.Store
	inbox storage.Inbox
	next  *nextRecorder
}

type nextRecorder struct{ called bool }

func newHarness(t *testing.T, mutate func(*storage.Inbox)) *harness {
	t.Helper()

	store := mem.New()

	inbox := storage.Inbox{
		ID:       uuid.New(),
		OwnerKey: "default",
		Name:     "test",
		// Must only use the token alphabet (no 0/O/1/l/I) - see storage.NewToken.
		Token:           "testtken7",
		Enabled:         true,
		ResponseCode:    200,
		SignatureHeader: "X-Signature",
		SignatureScheme: "hmac-sha256-hex",
	}

	if mutate != nil {
		mutate(&inbox)
	}

	if err := store.CreateInbox(context.Background(), &inbox); err != nil {
		t.Fatal(err)
	}

	bus := pubsub.NewInMemory[notify.Message]()

	h := harness{
		store: store,
		inbox: inbox,
		next:  &nextRecorder{},
	}

	h.deps = Deps{
		Log:        zap.NewNop(),
		Store:      store,
		Pub:        bus,
		Settings:   &config.AppSettings{MaxRequestBodySize: config.DefaultMaxRequestBodySize},
		TrustProxy: false,
	}

	return &h
}

func (h *harness) serve(method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	var next http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.next.called = true

		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(method, target, bytes.NewReader(body))

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()

	New(context.Background(), h.deps)(next).ServeHTTP(rec, req)

	return rec
}

func (h *harness) events(t *testing.T) []storage.EventListItem {
	t.Helper()

	items, _, err := h.store.ListEvents(context.Background(), storage.EventFilter{InboxID: h.inbox.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}

	return items
}

// ── size limit (the task: max 1 MiB, respond 413, do not store) ──────────────

func TestCapture_ExactlyAtLimitIsStored(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	body := bytes.Repeat([]byte("x"), 1<<20)

	rec := h.serve(http.MethodPost, "/hooks/testtken7", body, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("a body of exactly 1 MiB must be accepted, got %d", rec.Code)
	}

	if items := h.events(t); len(items) != 1 || items[0].BodySize != 1<<20 {
		t.Fatalf("expected one stored event of 1 MiB, got %+v", items)
	}
}

func TestCapture_OneByteOverLimitIsRejected(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	body := bytes.Repeat([]byte("x"), (1<<20)+1)

	rec := h.serve(http.MethodPost, "/hooks/testtken7", body, nil)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (%s)", rec.Code, rec.Body.String())
	}

	if items := h.events(t); len(items) != 0 {
		t.Fatalf("a rejected request must not be stored, got %d events", len(items))
	}
}

func TestCapture_HugeBodyIsNotBuffered(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	body := bytes.Repeat([]byte("x"), 10<<20) // 10 MiB

	start := time.Now()
	rec := h.serve(http.MethodPost, "/hooks/testtken7", body, nil)
	elapsed := time.Since(start)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}

	if elapsed > 2*time.Second {
		t.Fatalf("rejecting an oversized body took %s - it must not be read in full", elapsed)
	}

	if items := h.events(t); len(items) != 0 {
		t.Fatalf("nothing may be stored, got %d events", len(items))
	}
}

// ── token states ────────────────────────────────────────────────────────────

func TestCapture_UnknownTokenIs404(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	rec := h.serve(http.MethodPost, "/hooks/doesnotexist", []byte("{}"), nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestCapture_DisabledInboxIs403(t *testing.T) {
	t.Parallel()

	h := newHarness(t, func(in *storage.Inbox) { in.Enabled = false })

	rec := h.serve(http.MethodPost, "/hooks/testtken7", []byte("{}"), nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestCapture_NonHookPathGoesToNextHandler(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	h.serve(http.MethodGet, "/api/v1/inboxes", nil, nil)

	if !h.next.called {
		t.Fatal("a non /hooks path must be passed to the next handler")
	}
}

// ── body fidelity (the task: raw body must be preserved) ────────────────────

func TestCapture_BodyIsPreservedByteForByte(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	// Deliberately nasty: NUL bytes, invalid UTF-8, CRLF, high bytes.
	body := []byte{0x00, 0xff, 0xfe, 0x0d, 0x0a, 'a', 0x80, 0x81, 0x7f, 0x00}

	h.serve(http.MethodPost, "/hooks/testtken7", body, map[string]string{"Content-Type": "application/octet-stream"})

	items := h.events(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(items))
	}

	if !bytes.Equal(items[0].Body, body) {
		t.Fatalf("body was altered:\n got %v\nwant %v", items[0].Body, body)
	}
}

// ── signature handling ──────────────────────────────────────────────────────

func sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)

	return hex.EncodeToString(m.Sum(nil))
}

// TestCapture_RequireSignatureFailsClosed is the regression test for a fail-open bug:
// "require signature" without a configured secret used to accept everything.
func TestCapture_RequireSignatureFailsClosed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, func(in *storage.Inbox) { in.RequireSignature = true })

	rec := h.serve(http.MethodPost, "/hooks/testtken7", []byte("{}"), nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("require_signature without a secret must reject, got %d", rec.Code)
	}
}

func TestCapture_ValidSignatureAccepted(t *testing.T) {
	t.Parallel()

	secret := []byte("shh")

	h := newHarness(t, func(in *storage.Inbox) {
		in.RequireSignature = true
		in.SigningSecret = secret
	})

	body := []byte(`{"ok":true}`)

	rec := h.serve(http.MethodPost, "/hooks/testtken7", body, map[string]string{
		"X-Signature": sign(secret, body),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("valid signature must be accepted, got %d (%s)", rec.Code, rec.Body.String())
	}

	items := h.events(t)
	if len(items) != 1 || items[0].SignatureValid == nil || !*items[0].SignatureValid {
		t.Fatalf("event must be marked as signature valid, got %+v", items)
	}
}

func TestCapture_InvalidSignatureRejected(t *testing.T) {
	t.Parallel()

	secret := []byte("shh")

	h := newHarness(t, func(in *storage.Inbox) {
		in.RequireSignature = true
		in.SigningSecret = secret
	})

	rec := h.serve(http.MethodPost, "/hooks/testtken7", []byte(`{"ok":true}`), map[string]string{
		"X-Signature": sign([]byte("wrong"), []byte(`{"ok":true}`)),
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature must be rejected, got %d", rec.Code)
	}

	// The event is still recorded (marked invalid) so the user can debug why it failed.
	items := h.events(t)
	if len(items) != 1 || items[0].SignatureValid == nil || *items[0].SignatureValid {
		t.Fatalf("event must be recorded as invalid, got %+v", items)
	}
}

// ── client IP ───────────────────────────────────────────────────────────────

func TestCapture_ClientIPCannotBeSpoofed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	h.serve(http.MethodPost, "/hooks/testtken7", []byte("{}"), map[string]string{
		"X-Forwarded-For":  "8.8.8.8",
		"X-Wh-Trust-Proxy": "1",
	})

	items := h.events(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(items))
	}

	if items[0].ClientIP == "8.8.8.8" {
		t.Fatal("client IP must not be taken from request headers unless trust-proxy is enabled")
	}
}

// ── preflight ───────────────────────────────────────────────────────────────

func TestCapture_OptionsPreflightIsNotStored(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	rec := h.serve(http.MethodOptions, "/hooks/testtken7", nil, nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	if items := h.events(t); len(items) != 0 {
		t.Fatalf("a CORS preflight must not create an event, got %d", len(items))
	}
}

// ── sensitive headers ───────────────────────────────────────────────────────

func TestCapture_SensitiveHeadersAreMasked(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)

	h.serve(http.MethodPost, "/hooks/testtken7", []byte("{}"), map[string]string{
		"Authorization": "Bearer top-secret",
		"Cookie":        "sid=1",
	})

	items := h.events(t)
	if len(items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(items))
	}

	for _, hdr := range items[0].Headers {
		switch hdr.Name {
		case "Authorization", "Cookie":
			if hdr.Value == "Bearer top-secret" || hdr.Value == "sid=1" {
				t.Errorf("%s must not be stored in the clear", hdr.Name)
			}

			if !hdr.Sensitive {
				t.Errorf("%s must be flagged as sensitive", hdr.Name)
			}
		}
	}
}
