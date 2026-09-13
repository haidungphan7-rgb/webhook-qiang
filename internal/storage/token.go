package storage

import (
	"fmt"
	"strings"

	"github.com/yuandzhang/webhook-zq/internal/crypto"
)

// tokenAlphabet is a 31 character, unambiguous alphabet: no 0/O and no 1/l/I, lower case
// only. Inbox tokens are read aloud, pasted into chat and copied into third party
// dashboards - removing look-alike characters removes a whole class of support tickets.
// (Idea borrowed from Kjudeh/self-hosted-webhook-inspector.)
const tokenAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// tokenBytes is the amount of entropy bytes behind a token: 32 bytes = 256 bits.
const tokenBytes = 32

// NewToken returns a random, URL safe, unambiguous token of 51 characters (~255 bits of
// entropy). The token is the only secret protecting a receive URL until the optional
// HMAC signing is enabled, so it must be impossible to enumerate.
func NewToken() (string, error) {
	raw, err := crypto.RandomBytes(tokenBytes)
	if err != nil {
		return "", fmt.Errorf("cannot generate token: %w", err)
	}

	var (
		sb     strings.Builder
		buffer uint32 // accumulated bits
		bits   uint   // how many bits are currently in the buffer
	)

	sb.Grow((tokenBytes*8 + 4) / 5)

	for _, b := range raw {
		buffer = buffer<<8 | uint32(b)
		bits += 8

		for bits >= 5 {
			sb.WriteByte(tokenAlphabet[(buffer>>(bits-5))&0x1f])
			bits -= 5
		}
	}

	return sb.String(), nil
}

// IsValidToken reports whether s has the shape of a generated token. Used to reject
// obviously wrong input before hitting the database (e.g. path traversal attempts).
func IsValidToken(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}

	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(tokenAlphabet, rune(s[i])) {
			return false
		}
	}

	return true
}
