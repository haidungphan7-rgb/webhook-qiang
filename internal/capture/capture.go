// Package capture turns an inbound HTTP request into a storable event.
//
// Three rules drive everything here:
//
//  1. The body is stored byte for byte. No trimming, no re-encoding, no charset
//     "fixup". If the sender sent gzip bytes or invalid UTF-8, that is exactly what we
//     keep and what we will replay later.
//  2. Sensitive headers never reach the database in the clear. With an encryption key
//     configured they are sealed with AES-GCM (per inbox key); without one the value is
//     replaced by a mask. Either way the flag `sensitive` is stored so the UI can reveal
//     them explicitly and so the replay service can refuse to forward them.
//  3. The size limit is enforced while reading (http.MaxBytesReader), not after. Reading
//     the whole body first would let a single request allocate unbounded memory before we
//     get a chance to reject it.
package capture

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
	"github.com/yuandzhang/webhook-zq/internal/storage"
)

// MaskedValue replaces a sensitive header value when no encryption key is configured.
const MaskedValue = "***redacted***"

// EncryptedPrefix marks a header value that is AES-GCM sealed (base64 payload follows).
const EncryptedPrefix = "enc:v1:"

// SensitiveHeaders are never forwarded on replay and are masked/encrypted at rest.
// The first five are mandated by the task; the rest are the usual suspects.
var SensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"x-csrf-token":        true,
	// Same secrets under other common spellings: a provider that sends "Api-Key" instead
	// of "X-Api-Key" must not end up with the value in clear text.
	"api-key":                        true,
	"authentication":                 true,
	"x-access-token":                 true,
	"x-session-token":                true,
	"x-refresh-token":                true,
	"x-amz-security-token":           true,
	"x-goog-iam-authorization-token": true,
}

// IsSensitive reports whether a header name must be protected.
func IsSensitive(name string) bool {
	return SensitiveHeaders[strings.ToLower(strings.TrimSpace(name))]
}

// Headers converts an http.Header into the storage representation, protecting sensitive
// values. The result is sorted by name so the UI is stable across requests (Go's header
// map has a random iteration order, which would make diffs noisy).
func Headers(h http.Header, cipher *crypto.Cipher, inboxID uuid.UUID) []storage.HttpHeader {
	out := make([]storage.HttpHeader, 0, len(h))

	for name, values := range h {
		// A header can appear several times; keep the wire representation ("a; b") so the
		// detail view stays faithful to what was sent.
		value := strings.Join(values, "; ")

		if IsSensitive(name) {
			out = append(out, storage.HttpHeader{
				Name:      name,
				Value:     protect(cipher, inboxID, value),
				Sensitive: true,
			})

			continue
		}

		out = append(out, storage.HttpHeader{Name: name, Value: value})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// Reveal restores the original value of a protected header. Returns the value as stored
// when it cannot be decrypted (missing key, wrong key) so the UI never shows an empty box
// without explanation.
func Reveal(cipher *crypto.Cipher, inboxID uuid.UUID, h storage.HttpHeader) string {
	if !h.Sensitive {
		return h.Value
	}

	if cipher == nil || !strings.HasPrefix(h.Value, EncryptedPrefix) {
		return h.Value
	}

	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h.Value, EncryptedPrefix))
	if err != nil {
		return h.Value
	}

	plain, err := cipher.Decrypt(inboxID, blob)
	if err != nil {
		return h.Value
	}

	return string(plain)
}

func protect(cipher *crypto.Cipher, inboxID uuid.UUID, value string) string {
	if cipher == nil {
		return MaskedValue
	}

	blob, err := cipher.Encrypt(inboxID, []byte(value))
	if err != nil {
		// Failing closed: never store the secret just because crypto misbehaved.
		return MaskedValue
	}

	return EncryptedPrefix + base64.StdEncoding.EncodeToString(blob)
}
