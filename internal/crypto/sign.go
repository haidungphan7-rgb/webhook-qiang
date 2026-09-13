package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Schemes we understand when verifying an inbound signature.
const (
	// SchemeHex expects the raw hex digest, e.g. "6f2a…" (GitHub uses "sha256=<hex>").
	SchemeHex = "hmac-sha256-hex"
	// SchemeSha256Prefixed expects the "sha256=<hex>" form.
	SchemeSha256Prefixed = "sha256-prefixed"
)

// Sign returns the hex encoded HMAC-SHA256 of the body.
//
// Important: the MAC is always computed over the *raw* body bytes, never over a
// re-serialized ("pretty printed") version - re-encoding would change the bytes and
// produce a signature the receiver cannot verify.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(body)

	return hex.EncodeToString(m.Sum(nil))
}

// Verify compares a presented signature with the expected one in constant time.
//
// It accepts both "hex" and "sha256=hex" shapes so that real-world providers
// (GitHub, Stripe-style, most custom bots) work without extra configuration.
func Verify(secret, body []byte, presented string) bool {
	presented = strings.TrimSpace(presented)
	presented = strings.TrimPrefix(presented, "sha256=")

	if len(presented) != hex.EncodedLen(sha256.Size) {
		return false
	}

	given, err := hex.DecodeString(presented)
	if err != nil {
		return false
	}

	expected, err := hex.DecodeString(Sign(secret, body))
	if err != nil {
		return false
	}

	return hmac.Equal(given, expected)
}
