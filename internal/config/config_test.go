package config

import (
	"encoding/base64"
	"testing"
	"time"
)

func validSettings() AppSettings {
	return AppSettings{
		MaxRequestBodySize: DefaultMaxRequestBodySize,
		ReplayTimeout:      DefaultReplayTimeout,
		ReplayMaxPreview:   DefaultReplayMaxPreview,
		ReplayMaxRedirects: DefaultReplayMaxRedirects,
		ReplayMaxRetries:   DefaultReplayMaxRetries,
		ReplayBackoff:      DefaultReplayBackoff,
		RetentionMaxEvents: DefaultRetentionMaxEvents,
		RetentionMaxDays:   DefaultRetentionMaxDays,
		RetentionInterval:  DefaultRetentionInterval,
		MaxPageSize:        DefaultMaxPageSize,
	}
}

func TestValidate_AcceptsDefaults(t *testing.T) {
	t.Parallel()

	if err := validSettings().Validate(); err != nil {
		t.Fatalf("the default configuration must be valid: %v", err)
	}
}

func TestValidate_RejectsDangerousValues(t *testing.T) {
	t.Parallel()

	key32 := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key16 := base64.StdEncoding.EncodeToString(make([]byte, 16))

	cases := []struct {
		name   string
		mutate func(*AppSettings)
	}{
		{"zero body limit", func(s *AppSettings) { s.MaxRequestBodySize = 0 }},
		{"zero replay timeout", func(s *AppSettings) { s.ReplayTimeout = 0 }},
		{"zero preview", func(s *AppSettings) { s.ReplayMaxPreview = 0 }},
		{"negative redirects", func(s *AppSettings) { s.ReplayMaxRedirects = -1 }},
		{"too many retries", func(s *AppSettings) { s.ReplayMaxRetries = 6 }},
		{"negative retention", func(s *AppSettings) { s.RetentionMaxDays = -1 }},
		{"zero retention interval", func(s *AppSettings) { s.RetentionInterval = 0 }},
		{"huge page size", func(s *AppSettings) { s.MaxPageSize = 501 }},
		{"key not base64", func(s *AppSettings) { s.EncryptKey = "not-base64!!" }},
		{"key too short", func(s *AppSettings) { s.EncryptKey = key16 }},
		{"public url without scheme", func(s *AppSettings) { s.PublicURLRoot = "example.com" }},
		{"public url with credentials", func(s *AppSettings) { s.PublicURLRoot = "https://u:p@example.com" }},
		{"allow host is a private address", func(s *AppSettings) { s.ReplayAllowHosts = []string{"10.0.0.5"} }},
		{"allow host is loopback", func(s *AppSettings) { s.ReplayAllowHosts = []string{"127.0.0.1"} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := validSettings()
			tc.mutate(&s)

			if err := s.Validate(); err == nil {
				t.Fatalf("expected %q to be rejected", tc.name)
			}
		})
	}

	// A valid key must pass.
	s := validSettings()
	s.EncryptKey = key32

	if err := s.Validate(); err != nil {
		t.Fatalf("a valid key must be accepted: %v", err)
	}

	// The same private host is allowed when AllowPrivate is explicitly enabled.
	s = validSettings()
	s.ReplayAllowHosts = []string{"127.0.0.1"}
	s.ReplayAllowPrivate = true

	if err := s.Validate(); err != nil {
		t.Fatalf("allow private must permit a reserved host: %v", err)
	}
}

func TestValidateKey(t *testing.T) {
	t.Parallel()

	if err := ValidateKey(""); err == nil {
		t.Error("an empty key must be rejected")
	}

	if err := ValidateKey(base64.StdEncoding.EncodeToString(make([]byte, 32))); err != nil {
		t.Errorf("a 32 byte key must be accepted: %v", err)
	}

	if err := ValidateKey("definitely not base64"); err == nil {
		t.Error("a non base64 key must be rejected")
	}
}

func TestDefaultsAreSane(t *testing.T) {
	t.Parallel()

	if DefaultMaxRequestBodySize != 1<<20 {
		t.Errorf("the task requires a 1 MiB body limit, got %d", DefaultMaxRequestBodySize)
	}

	if DefaultReplayTimeout <= 0 || DefaultReplayTimeout > 60*time.Second {
		t.Errorf("unexpected default replay timeout %s", DefaultReplayTimeout)
	}
}
