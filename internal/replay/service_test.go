package replay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

// testFixture wires a replay service whose HTTP client is pinned to one local server,
// while the target validation still runs with the real production policy.
func testFixture(t *testing.T, handler http.Handler) (*Service, *mem.Store, storage.Inbox, storage.Event, string) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	store := mem.New()

	inbox := storage.Inbox{
		ID:              uuid.New(),
		Name:            "test",
		Token:           "tok",
		Enabled:         true,
		SignatureHeader: "X-Signature",
		SignatureScheme: crypto.SchemeHex,
	}

	if err = store.CreateInbox(context.Background(), &inbox); err != nil {
		t.Fatal(err)
	}

	event := storage.Event{
		ID:          uuid.New(),
		InboxID:     inbox.ID,
		Method:      http.MethodPost,
		Path:        "/hooks/tok",
		ContentType: "application/json",
		Body:        []byte(`{"a":1}`),
		Headers: []storage.HttpHeader{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "X-Trace-Id", Value: "abc"},
			{Name: "Authorization", Value: "Bearer secret", Sensitive: true},
			{Name: "Cookie", Value: "sid=1", Sensitive: true},
			{Name: "X-Api-Key", Value: "key", Sensitive: true},
			{Name: "Host", Value: "original.example.com"},
			// A deliberately wrong length: forwarding it verbatim would break the request.
			{Name: "Content-Length", Value: "999"},
		},
	}

	if err = store.CreateEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}

	policy := Policy{
		Timeout:      2 * time.Second,
		MaxPreview:   4096,
		MaxRedirects: 0,
		lookupIP:     fakeDNS(map[string][]string{"target.test": {"1.1.1.1"}}),
		dialer:       dialTo("127.0.0.1:" + port),
	}

	svc := New(zap.NewNop(), store, policy)

	return svc, store, inbox, event, "http://target.test/hook"
}

func TestRun_Success(t *testing.T) {
	t.Parallel()

	svc, store, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}

	a := attempts[0]

	if a.Outcome != storage.OutcomeSuccess || a.StatusCode == nil || *a.StatusCode != 200 {
		t.Fatalf("expected success/200, got %s %v", a.Outcome, a.StatusCode)
	}

	if a.ResponsePreview != "ok" {
		t.Fatalf("unexpected preview %q", a.ResponsePreview)
	}

	stored, err := store.ListReplays(context.Background(), event.ID, 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("attempt was not persisted: %v %v", stored, err)
	}
}

func TestRun_HTTPErrorIsRecorded(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := attempts[0]

	if a.Outcome != storage.OutcomeHTTPError || a.StatusCode == nil || *a.StatusCode != 500 {
		t.Fatalf("expected http_error/500, got %s %v", a.Outcome, a.StatusCode)
	}

	if a.ResponsePreview != "boom" {
		t.Fatalf("the non 2xx response must still be recorded, got %q", a.ResponsePreview)
	}
}

func TestRun_Timeout(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	svc.policy.Timeout = 100 * time.Millisecond
	svc.client = svc.policy.Client()

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if attempts[0].Outcome != storage.OutcomeTimeout {
		t.Fatalf("expected timeout, got %s (%s)", attempts[0].Outcome, attempts[0].Error)
	}
}

func TestRun_NetworkError(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, _ := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	svc.policy.dialer = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	svc.client = svc.policy.Client()

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://target.test/hook"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if attempts[0].Outcome != storage.OutcomeNetworkError {
		t.Fatalf("expected network_error, got %s", attempts[0].Outcome)
	}
}

// TestRun_BlockedIsRecorded is the executable proof that the target restriction is
// verifiable: a refused target is rejected AND leaves an audit record.
func TestRun_BlockedIsRecorded(t *testing.T) {
	t.Parallel()

	store := mem.New()

	inbox := storage.Inbox{ID: uuid.New(), Name: "x", Token: "t", Enabled: true}
	if err := store.CreateInbox(context.Background(), &inbox); err != nil {
		t.Fatal(err)
	}

	event := storage.Event{ID: uuid.New(), InboxID: inbox.ID, Method: http.MethodPost, Body: []byte("{}")}
	if err := store.CreateEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}

	svc := New(zap.NewNop(), store, Policy{Timeout: time.Second})

	_, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://169.254.169.254/latest"})
	if !errors.Is(err, ErrBlockedTarget) {
		t.Fatalf("expected ErrBlockedTarget, got %v", err)
	}

	stored, err := store.ListReplays(context.Background(), event.ID, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(stored) != 1 || stored[0].Outcome != storage.OutcomeBlocked {
		t.Fatalf("the refusal must be recorded, got %+v", stored)
	}
}

func TestRequest_Headers(t *testing.T) {
	t.Parallel()

	var received http.Header

	svc, _, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()

		w.WriteHeader(http.StatusOK)
	}))

	if _, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target}); err != nil {
		t.Fatal(err)
	}

	forbidden := []string{"Authorization", "Cookie", "X-Api-Key", "X-Forwarded-For"}

	for _, name := range forbidden {
		if received.Get(name) != "" {
			t.Errorf("header %q must not be forwarded, got %q", name, received.Get(name))
		}
	}

	// The captured Host must not be reused (Go sets the real target host).
	if received.Get("Host") == "original.example.com" {
		t.Errorf("captured Host must not be forwarded, got %q", received.Get("Host"))
	}

	// Content-Length must be recomputed for the bytes actually sent, not copied (the
	// captured value is deliberately wrong: 999 vs. 7 bytes).
	if received.Get("Content-Length") == "999" || received.Get("Content-Length") != "7" {
		t.Errorf("Content-Length must be recomputed, got %q", received.Get("Content-Length"))
	}

	if received.Get("X-Trace-Id") != "abc" {
		t.Errorf("safe custom header must be forwarded, got %q", received.Get("X-Trace-Id"))
	}

	if received.Get("Content-Type") != "application/json" {
		t.Errorf("content type must be preserved, got %q", received.Get("Content-Type"))
	}
}

// TestRequest_SignUsesRawBytes verifies both the signature scheme and the fact that the
// MAC is computed over the original bytes (not over a re-encoded body).
func TestRequest_SignUsesRawBytes(t *testing.T) {
	t.Parallel()

	secret := []byte("s3cr3t")

	var signature string

	svc, _, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature = r.Header.Get("X-Signature")

		w.WriteHeader(http.StatusOK)
	}))

	inbox.SigningSecret = secret

	if _, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target, Sign: true}); err != nil {
		t.Fatal(err)
	}

	if !crypto.Verify(secret, event.Body, signature) {
		t.Fatalf("signature %q does not verify over the raw body", signature)
	}

	// A re-encoded body (e.g. pretty printed JSON) must NOT produce the same signature.
	if crypto.Verify(secret, []byte("{\n  \"a\": 1\n}"), signature) {
		t.Fatal("signature must be bound to the exact bytes")
	}
}

// A 2xx whose body never arrived is not a success: the user asked to see the response,
// and recording "success" would hide a broken target behind a green badge.
func TestRun_UnreadableBodyIsNotSuccess(t *testing.T) {
	t.Parallel()

	svc, store, inbox, event, target := testFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("hijacking is not supported by the test server")

			return
		}

		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))

	attempts, _ := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: target})
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}

	if attempts[0].Outcome == storage.OutcomeSuccess {
		t.Fatalf("a 2xx with an unreadable body must not be recorded as success: %+v", attempts[0])
	}

	if attempts[0].Error == "" {
		t.Fatal("the failure must be visible in the attempt error")
	}

	stored, sErr := store.ListReplays(context.Background(), event.ID, 10)
	if sErr != nil || len(stored) != 1 || stored[0].Outcome == storage.OutcomeSuccess {
		t.Fatalf("the persisted attempt must carry the same outcome: %+v (%v)", stored, sErr)
	}
}

func TestReadPreview(t *testing.T) {
	t.Parallel()

	if got, truncated, err := readPreview(strings.NewReader("0123456789"), 8); err != nil || got != "01234567" || !truncated {
		t.Fatalf("got %q %v %v", got, truncated, err)
	}

	if got, truncated, err := readPreview(strings.NewReader("0123"), 8); err != nil || got != "0123" || truncated {
		t.Fatalf("got %q %v %v", got, truncated, err)
	}

	if got, _, err := readPreview(strings.NewReader("0123"), 0); err != nil || len(got) != 4 {
		t.Fatalf("max=0 must fall back to the default, got %d bytes (err=%v)", len(got), err)
	}

	// A transfer that dies half way must be reported, not swallowed: the service turns
	// it into network_error, and a 2xx with an unreadable body is never "success".
	got, truncated, err := readPreview(&failingReader{}, 8)
	if err == nil {
		t.Fatal("a failing reader must report an error")
	}

	if !truncated {
		t.Fatal("a partially read body must be flagged as truncated")
	}

	if got == "" {
		t.Fatal("whatever arrived before the failure must be kept for debugging")
	}
}

// failingReader yields one chunk and then fails, like a connection reset mid-body.
type failingReader struct{ calls int }

func (r *failingReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 1 {
		return copy(p, "partial"), nil
	}

	return 0, errors.New("connection reset by peer")
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	code := func(v int) *int { return &v }

	if !retryable(storage.OutcomeTimeout, nil) {
		t.Error("timeout must be retryable")
	}

	if !retryable(storage.OutcomeHTTPError, code(503)) {
		t.Error("5xx must be retryable")
	}

	if retryable(storage.OutcomeHTTPError, code(404)) {
		t.Error("4xx must not be retryable")
	}

	if retryable(storage.OutcomeSuccess, code(200)) {
		t.Error("success must not be retried")
	}
}
