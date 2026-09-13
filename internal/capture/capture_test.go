package capture

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
)

func TestHeaders_MaskedWithoutCipher(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("Cookie", "sid=1")
	h.Set("X-Trace-Id", "abc")

	out := Headers(h, nil, uuid.New())

	byName := map[string]string{}

	for _, hdr := range out {
		byName[hdr.Name] = hdr.Value
	}

	if byName["Authorization"] != MaskedValue {
		t.Fatalf("authorization must be masked, got %q", byName["Authorization"])
	}

	if byName["Cookie"] != MaskedValue {
		t.Fatalf("cookie must be masked, got %q", byName["Cookie"])
	}

	if byName["X-Trace-Id"] != "abc" {
		t.Fatalf("a normal header must be kept, got %q", byName["X-Trace-Id"])
	}

	for _, hdr := range out {
		if hdr.Name == "Authorization" && !hdr.Sensitive {
			t.Fatal("authorization must be flagged sensitive")
		}
	}
}

func TestHeaders_EncryptedWithCipher(t *testing.T) {
	t.Parallel()

	cipher, err := crypto.NewCipher(makeKey())
	if err != nil {
		t.Fatal(err)
	}

	id := uuid.New()

	h := http.Header{}
	h.Set("Authorization", "Bearer secret")

	out := Headers(h, cipher, id)

	if !strings.HasPrefix(out[0].Value, EncryptedPrefix) {
		t.Fatalf("value must be encrypted, got %q", out[0].Value)
	}

	if strings.Contains(out[0].Value, "secret") {
		t.Fatal("the plaintext must not be visible")
	}

	if got := Reveal(cipher, id, out[0]); got != "Bearer secret" {
		t.Fatalf("reveal must restore the value, got %q", got)
	}
}

func TestReveal_WrongInboxReturnsStoredValue(t *testing.T) {
	t.Parallel()

	cipher, err := crypto.NewCipher(makeKey())
	if err != nil {
		t.Fatal(err)
	}

	id := uuid.New()

	h := http.Header{}
	h.Set("Authorization", "Bearer secret")

	out := Headers(h, cipher, id)

	if got := Reveal(cipher, uuid.New(), out[0]); got != out[0].Value {
		t.Fatalf("revealing with another inbox must fail closed, got %q", got)
	}

	if got := Reveal(nil, id, out[0]); got != out[0].Value {
		t.Fatalf("revealing without a cipher must return the stored value, got %q", got)
	}
}

func TestHeaders_AreSortedByName(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Z-Header", "1")
	h.Set("A-Header", "2")
	h.Set("M-Header", "3")

	out := Headers(h, nil, uuid.New())

	for i := 1; i < len(out); i++ {
		if out[i-1].Name > out[i].Name {
			t.Fatalf("headers must be sorted, got %v", out)
		}
	}
}

// TestHeaders_MultipleValuesAreJoined verifies that repeated headers keep their wire
// representation instead of being silently reduced to the first value.
func TestHeaders_MultipleValuesAreJoined(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Add("X-Multi", "one")
	h.Add("X-Multi", "two")

	out := Headers(h, nil, uuid.New())

	if len(out) != 1 || out[0].Value != "one; two" {
		t.Fatalf("expected joined values, got %+v", out)
	}
}

func TestIsSensitive(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"authorization", "Cookie", "X-API-Key", "Proxy-Authorization", "Set-Cookie"} {
		if !IsSensitive(name) {
			t.Errorf("%q must be sensitive", name)
		}
	}

	if IsSensitive("X-Trace-Id") || IsSensitive("Content-Type") {
		t.Error("ordinary headers must not be sensitive")
	}
}

func makeKey() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}

	return b
}
