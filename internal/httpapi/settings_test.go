package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// SettingsFields is the exact key set the UI is allowed to rely on.
//
// It is a literal list rather than something derived from the struct on purpose: deriving
// it would make the test a tautology. Adding a field to SettingsDTO without adding it here
// fails the test, which is the whole point - it forces you to add it to
// web/src/api/v1.ts too.
var SettingsFields = []string{
	"max_request_body_size", "replay_timeout_ms", "replay_max_preview",
	"replay_max_redirects", "replay_max_retries", "replay_rate_limit",
	"retention_max_events", "retention_max_days", "auth_enabled",
	"encryption_enabled", "public_url_root", "sensitive_headers",
	"replay_allow_hosts", "replay_allow_private", "version", "build_time",
}

func TestSettings_PayloadHasExactlyTheDocumentedFields(t *testing.T) {
	t.Parallel()

	// ReplayAllowHosts stays nil on purpose: that is the case that used to serialise to
	// null and blank the help screen.
	_, api, _, _ := fixture(t, func(s *config.AppSettings) { s.ReplayAllowHosts = nil })

	rec := do(t, api, http.MethodGet, "/v1/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/settings = %d, want 200", rec.Code)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("/v1/settings is not a JSON object: %v", err)
	}

	want := make(map[string]bool, len(SettingsFields))
	for _, k := range SettingsFields {
		want[k] = true
	}

	for k := range got {
		if !want[k] {
			t.Errorf("field %q is not in SettingsFields - add it there AND to web/src/api/v1.ts", k)
		}
	}

	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("field %q is documented but missing from the payload", k)
		}
	}

	// The two slices that broke the help screen once.
	for _, k := range []string{"sensitive_headers", "replay_allow_hosts"} {
		if string(got[k]) == "null" {
			t.Errorf("%s serialised as null; the UI does .length on it and will throw", k)
		}
	}
}
