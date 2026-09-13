package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yuandzhang/webhook-zq/internal/capture"
	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

// These cover the branches that were changed or added late and had no assertion at all:
// the reveal flag (three states), reveal in the JSON export, the heredoc delimiter, and
// the websocket origin check the README publicly claims.

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()

	c, err := crypto.NewCipher(bytes32())
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func bytes32() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}

	return b
}

// encrypted mimics capture.protect (which is unexported): EncryptedPrefix + base64.
func encrypted(t *testing.T, c *crypto.Cipher, inboxID uuid.UUID, value string) string {
	t.Helper()

	blob, err := c.Encrypt(inboxID, []byte(value))
	if err != nil {
		t.Fatal(err)
	}

	return capture.EncryptedPrefix + base64.StdEncoding.EncodeToString(blob)
}

func newEvent(t *testing.T, store *mem.Store, inboxID uuid.UUID, authValue, body string) storage.Event {
	t.Helper()

	ev := storage.Event{
		ID:          uuid.New(),
		InboxID:     inboxID,
		Method:      http.MethodPost,
		Path:        "/hooks/x",
		ContentType: "application/json",
		Body:        []byte(body),
		Headers: []storage.HttpHeader{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "Authorization", Value: authValue, Sensitive: true},
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := store.CreateEvent(context.Background(), &ev); err != nil {
		t.Fatal(err)
	}

	return ev
}

type headerDTO struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
	Revealed  bool   `json:"revealed"`
}

func sensitiveHeader(t *testing.T, rec *httptest.ResponseRecorder) headerDTO {
	t.Helper()

	out := decode[struct {
		Headers []headerDTO `json:"headers"`
	}](t, rec)

	for _, h := range out.Headers {
		if h.Sensitive {
			return h
		}
	}

	t.Fatal("no sensitive header in the response")

	return headerDTO{}
}

// TestEventDetail_RevealStates pins the three states of ?reveal=1.
//
// Asserting only the value is not enough: a test that checks "the value is not the
// plaintext" would also pass if decryption silently did nothing. Both the value and the
// revealed flag have to be asserted together.
func TestEventDetail_RevealStates(t *testing.T) {
	t.Parallel()

	cip := testCipher(t)

	t.Run("decrypts when access control and a key are configured", func(t *testing.T) {
		t.Parallel()

		store, api, inbox, _ := fixture(t, func(s *config.AppSettings) { s.AuthToken = "tok" }, withCipher(cip))
		ev := newEvent(t, store, inbox.ID, encrypted(t, cip, inbox.ID, "Bearer top-secret"), `{"a":1}`)

		rec := do(t, api, http.MethodGet, "/v1/events/"+ev.ID.String()+"?reveal=1", "",
			"Authorization", "Bearer tok")

		h := sensitiveHeader(t, rec)
		if h.Value != "Bearer top-secret" {
			t.Fatalf("expected the plaintext, got %q", h.Value)
		}

		if !h.Revealed {
			t.Fatal("revealed must be true when the value was actually decrypted")
		}
	})

	t.Run("stays masked when the original was never recoverable", func(t *testing.T) {
		t.Parallel()

		// A value stored before --encrypt-key existed is an irreversible mask. Revealing
		// it must not pretend anything happened.
		store, api, inbox, _ := fixture(t, func(s *config.AppSettings) { s.AuthToken = "tok" }, withCipher(cip))
		ev := newEvent(t, store, inbox.ID, capture.MaskedValue, `{"a":1}`)

		rec := do(t, api, http.MethodGet, "/v1/events/"+ev.ID.String()+"?reveal=1", "",
			"Authorization", "Bearer tok")

		h := sensitiveHeader(t, rec)
		if h.Value != capture.MaskedValue {
			t.Fatalf("expected the mask to survive, got %q", h.Value)
		}

		if h.Revealed {
			t.Fatal("revealed must be false: nothing was actually decrypted")
		}
	})

	t.Run("ignored when access control is off", func(t *testing.T) {
		t.Parallel()

		store, api, inbox, _ := fixture(t, nil, withCipher(cip))
		ev := newEvent(t, store, inbox.ID, encrypted(t, cip, inbox.ID, "Bearer top-secret"), `{"a":1}`)

		rec := do(t, api, http.MethodGet, "/v1/events/"+ev.ID.String()+"?reveal=1", "")

		h := sensitiveHeader(t, rec)
		if h.Value != "" && h.Value != capture.MaskedValue && !strings.HasPrefix(h.Value, capture.EncryptedPrefix) {
			t.Fatalf("without access control the plaintext must never be returned, got %q", h.Value)
		}

		if h.Revealed {
			t.Fatal("revealed must be false when access control is disabled")
		}
	})
}

// TestExport_JSONHonoursReveal covers the JSON export, which used to compute reveal and
// then not use it - an export that always ships masked secrets is useless as a repro case.
func TestExport_JSONHonoursReveal(t *testing.T) {
	t.Parallel()

	cip := testCipher(t)
	store, api, inbox, _ := fixture(t, func(s *config.AppSettings) { s.AuthToken = "tok" }, withCipher(cip))
	ev := newEvent(t, store, inbox.ID, encrypted(t, cip, inbox.ID, "Bearer top-secret"), `{"a":1}`)

	withReveal := do(t, api, http.MethodGet,
		"/v1/events/"+ev.ID.String()+"/export?format=json&reveal=1", "", "Authorization", "Bearer tok")
	if got := sensitiveHeader(t, withReveal).Value; got != "Bearer top-secret" {
		t.Fatalf("export with reveal must contain the plaintext, got %q", got)
	}

	without := do(t, api, http.MethodGet,
		"/v1/events/"+ev.ID.String()+"/export?format=json", "", "Authorization", "Bearer tok")
	if got := sensitiveHeader(t, without).Value; got == "Bearer top-secret" {
		t.Fatal("export without reveal must not contain the plaintext")
	}
}

// TestExport_CurlHeredocDelimiterAvoidsBody covers the A22 fix: a heredoc breaks if the
// payload itself contains the delimiter on its own line.
func TestExport_CurlHeredocDelimiterAvoidsBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"plain body keeps EOF", `{"a":1}`, "EOF"},
		{"body with EOF on its own line", "line1\nEOF\nline3", "EOF_"},
		{"body with EOF and EOF_", "a\nEOF\nb\nEOF_\nc", "EOF__"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, api, inbox, _ := fixture(t, nil)
			ev := newEvent(t, store, inbox.ID, capture.MaskedValue, tc.body)

			rec := do(t, api, http.MethodGet, "/v1/events/"+ev.ID.String()+"/export?format=curl", "")

			// The cURL export is plain text (so it can be pasted straight into a
			// terminal), not a JSON envelope.
			command := rec.Body.String()

			if !strings.Contains(command, "<<'"+tc.want+"'") {
				t.Fatalf("expected delimiter %q in:\n%s", tc.want, command)
			}

			if !strings.HasSuffix(strings.TrimRight(command, "\n"), "\n"+tc.want) {
				t.Fatalf("the closing line must use the same delimiter %q:\n%s", tc.want, command)
			}

			if !strings.Contains(command, tc.body) {
				t.Fatalf("the body must survive intact:\n%s", command)
			}
		})
	}
}

// TestSameOrigin is the whitelist the README advertises: "cross site pages cannot
// subscribe". It was never asserted, which would make that claim unverified.
func TestSameOrigin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin (script client)", "", "example.com", true},
		{"same host", "http://example.com", "example.com", true},
		{"same host, case differs", "HTTP://EXAMPLE.COM", "example.com", true},
		{"different host", "http://evil.example", "example.com", false},
		{"different port", "http://example.com:8080", "example.com", false},
		{"subdomain is not the same origin", "http://a.example.com", "example.com", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/v1/inboxes/x/events/subscribe", nil)
			req.Host = tc.host

			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}

			if got := sameOrigin(req); got != tc.want {
				t.Fatalf("origin %q against host %q: got %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}
