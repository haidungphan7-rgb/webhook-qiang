package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/replay"
	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

// fixture builds a full API (memory store, in-memory bus, real replay policy) plus one
// inbox and one captured event.
//
// The API layer had no tests at all, which meant every status code, error code and the
// tenant isolation rules were only ever checked by hand. This fixture makes them cheap
// to assert: no PostgreSQL, no network.
// fixtureOpt tweaks the dependencies after they have been assembled. It exists so a test
// can inject things the settings struct cannot express (a cipher, a nil PubSub).
type fixtureOpt func(*Deps)

// withCipher enables secret decryption, which is what makes ?reveal=1 meaningful.
func withCipher(c *crypto.Cipher) fixtureOpt {
	return func(d *Deps) { d.Cipher = c }
}

func fixture(t *testing.T, settings func(*config.AppSettings), opts ...fixtureOpt) (*mem.Store, http.Handler, storage.Inbox, storage.Event) {
	t.Helper()

	store := mem.New()
	ctx := context.Background()

	inbox := storage.Inbox{
		ID:              uuid.New(),
		OwnerKey:        defaultOwnerKey,
		Name:            "demo",
		Token:           "abc",
		Enabled:         true,
		RetentionMaxEvents: 500,
		ResponseCode:    200,
	}

	if err := store.CreateInbox(ctx, &inbox); err != nil {
		t.Fatal(err)
	}

	event := storage.Event{
		ID:          uuid.New(),
		InboxID:     inbox.ID,
		Method:      http.MethodPost,
		Path:        "/hooks/abc",
		ContentType: "application/json",
		Body:        []byte(`{"a":1}`),
		Headers: []storage.HttpHeader{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "Authorization", Value: "***redacted***", Sensitive: true},
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := store.CreateEvent(ctx, &event); err != nil {
		t.Fatal(err)
	}

	s := config.AppSettings{
		MaxRequestBodySize: config.DefaultMaxRequestBodySize,
		ReplayTimeout:      2 * time.Second,
		ReplayMaxPreview:   config.DefaultReplayMaxPreview,
		ReplayRateLimit:    config.DefaultReplayRateLimit,
		RetentionMaxEvents: config.DefaultRetentionMaxEvents,
		RetentionMaxDays:   config.DefaultRetentionMaxDays,
		RetentionInterval:  config.DefaultRetentionInterval,
		MaxPageSize:        config.DefaultMaxPageSize,
	}

	if settings != nil {
		settings(&s)
	}

	svc := replay.New(zap.NewNop(), store, replay.Policy{
		Timeout:    s.ReplayTimeout,
		MaxPreview: s.ReplayMaxPreview,
	})

	deps := Deps{
		Log:      zap.NewNop(),
		Settings: &s,
		Store:    store,
		Replay:   svc,
		PubSub:   pubsub.NewInMemory[notify.Message](),
	}

	for _, opt := range opts {
		opt(&deps)
	}

	return store, New(deps), inbox, event
}

func do(t *testing.T, h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	var r io.Reader = strings.NewReader(body)

	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("cannot decode %s: %v", rec.Body.String(), err)
	}

	return out
}

func TestCreateInbox_Validation(t *testing.T) {
	t.Parallel()

	_, api, _, _ := fixture(t, nil)

	if rec := do(t, api, http.MethodPost, "/v1/inboxes", `{"name":"  "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank name must be rejected, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec := do(t, api, http.MethodPost, "/v1/inboxes", `{"name":"ok"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("expected 2xx, got %d (%s)", rec.Code, rec.Body.String())
	}

	var in map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &in)

	if _, ok := in["token"]; !ok {
		t.Fatal("the response must expose the receive token")
	}
}

func TestListInboxes_FiltersAndPaging(t *testing.T) {
	t.Parallel()

	store, api, _, _ := fixture(t, nil)
	ctx := context.Background()

	second := storage.Inbox{ID: uuid.New(), OwnerKey: defaultOwnerKey, Name: "another", Token: "def", Enabled: false, ResponseCode: 200}
	if err := store.CreateInbox(ctx, &second); err != nil {
		t.Fatal(err)
	}

	type paged struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}

	all := decode[paged](t, do(t, api, http.MethodGet, "/v1/inboxes", ""))
	if all.Total != 2 {
		t.Fatalf("expected 2 inboxes, got %d", all.Total)
	}

	byName := decode[paged](t, do(t, api, http.MethodGet, "/v1/inboxes?q=anoth", ""))
	if byName.Total != 1 {
		t.Fatalf("q filter should match 1, got %d", byName.Total)
	}

	disabled := decode[paged](t, do(t, api, http.MethodGet, "/v1/inboxes?enabled=false", ""))
	if disabled.Total != 1 {
		t.Fatalf("enabled=false should match 1, got %d", disabled.Total)
	}

	// The page size is clamped server side and the clamped value is echoed back.
	clamped := decode[paged](t, do(t, api, http.MethodGet, "/v1/inboxes?limit=5000", ""))
	if clamped.Total != 2 {
		t.Fatalf("clamped list should still work, got %d", clamped.Total)
	}
}

func TestInboxAccess_IsScopedToTenant(t *testing.T) {
	t.Parallel()

	_, api, inbox, _ := fixture(t, func(s *config.AppSettings) {
		s.AuthKeys = map[string]string{"alice": "kA", "bob": "kB"}
	})

	// No key at all.
	if rec := do(t, api, http.MethodGet, "/v1/inboxes/"+inbox.ID.String(), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request must be 401, got %d", rec.Code)
	}

	// bob must not see alice's inbox, and must not learn that it exists.
	rec := do(t, api, http.MethodGet, "/v1/inboxes/"+inbox.ID.String(), "", "Authorization", "Bearer kB")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another tenant's inbox must be 404 (not 403), got %d", rec.Code)
	}
}

func TestPatchInbox_RejectsImpossibleResponses(t *testing.T) {
	t.Parallel()

	_, api, inbox, _ := fixture(t, nil)

	// 204 cannot carry the event id in a body, so it must be refused with an explanation.
	rec := do(t, api, http.MethodPatch, "/v1/inboxes/"+inbox.ID.String(), `{"response_code":204}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("204 must be rejected, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = do(t, api, http.MethodPatch, "/v1/inboxes/"+inbox.ID.String(), `{"signature_scheme":"md5"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown signature scheme must be rejected, got %d", rec.Code)
	}

	rec = do(t, api, http.MethodPatch, "/v1/inboxes/"+inbox.ID.String(), `{"response_code":418}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid patch must succeed, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchInbox_OnlyTouchesWhatWasSent(t *testing.T) {
	t.Parallel()

	_, api, inbox, _ := fixture(t, nil)

	if rec := do(t, api, http.MethodPatch, "/v1/inboxes/"+inbox.ID.String(), `{"name":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch failed: %s", rec.Body.String())
	}

	got := decode[map[string]any](t, do(t, api, http.MethodGet, "/v1/inboxes/"+inbox.ID.String(), ""))

	if got["name"] != "renamed" {
		t.Fatalf("name was not applied: %v", got["name"])
	}

	if got["response_code"] != float64(200) {
		t.Fatalf("an unrelated field changed: response_code=%v", got["response_code"])
	}
}

func TestReplay_BlockedTargetIsRecorded(t *testing.T) {
	t.Parallel()

	store, api, _, event := fixture(t, nil)

	// The cloud metadata address is the canonical SSRF target: the request must be
	// refused AND the refusal must be visible in the history.
	rec := do(t, api, http.MethodPost, "/v1/events/"+event.ID.String()+"/replay",
		`{"target_url":"http://169.254.169.254/latest/meta-data/"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (%s)", rec.Code, rec.Body.String())
	}

	attempts := decode[struct {
		Items []struct {
			Outcome string `json:"outcome"`
		} `json:"items"`
	}](t, do(t, api, http.MethodGet, "/v1/events/"+event.ID.String()+"/replays", ""))

	if len(attempts.Items) == 0 || attempts.Items[0].Outcome != storage.OutcomeBlocked {
		t.Fatalf("the blocked attempt must be persisted, got %+v", attempts.Items)
	}

	_ = store
}

func TestReplay_RateLimit(t *testing.T) {
	t.Parallel()

	_, api, _, event := fixture(t, func(s *config.AppSettings) { s.ReplayRateLimit = 2 })

	url := "/v1/events/" + event.ID.String() + "/replay"

	for i := 1; i <= 2; i++ {
		if rec := do(t, api, http.MethodPost, url, `{"target_url":"http://169.254.169.254/"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d should be rejected by the policy (403), not %d", i, rec.Code)
		}
	}

	rec := do(t, api, http.MethodPost, url, `{"target_url":"http://169.254.169.254/"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after the budget is spent, got %d", rec.Code)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry a Retry-After header")
	}
}

func TestEventDetail_MasksSensitiveHeaders(t *testing.T) {
	t.Parallel()

	_, api, _, event := fixture(t, nil)

	got := decode[struct {
		Headers []struct {
			Name      string `json:"name"`
			Value     string `json:"value"`
			Sensitive bool   `json:"sensitive"`
		} `json:"headers"`
	}](t, do(t, api, http.MethodGet, "/v1/events/"+event.ID.String(), ""))

	var found bool

	for _, h := range got.Headers {
		if strings.EqualFold(h.Name, "authorization") {
			found = true

			if !h.Sensitive {
				t.Error("authorization must be flagged as sensitive")
			}

			if strings.Contains(h.Value, "Bearer") {
				t.Errorf("secret leaked: %q", h.Value)
			}
		}
	}

	if !found {
		t.Fatal("authorization header missing from the detail response")
	}
}

func TestExport_CurlContainsTheBody(t *testing.T) {
	t.Parallel()

	_, api, _, event := fixture(t, nil)

	rec := do(t, api, http.MethodGet, "/v1/events/"+event.ID.String()+"/export?format=curl", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export failed: %d", rec.Code)
	}

	body := rec.Body.String()

	if !strings.Contains(body, "curl -i -X POST") {
		t.Fatalf("unexpected export: %s", body)
	}

	if !strings.Contains(body, `{"a":1}`) {
		t.Fatal("the exported command must carry the original body")
	}

	if strings.Contains(body, "Bearer") {
		t.Fatal("the exported command must not carry the real secret")
	}
}

func TestUnknownRoute(t *testing.T) {
	t.Parallel()

	_, api, _, _ := fixture(t, nil)

	if rec := do(t, api, http.MethodGet, "/v1/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
