package config

import (
	"encoding/base64"
	"errors"
	"fmt"
)

// KeyLen is the required length of the master encryption key (AES-256).
const KeyLen = 32

// ValidateKey checks that the provided value is a base64 encoded 32 byte key.
//
// Keeping the check next to the config (instead of inside the crypto package) avoids a
// config -> crypto dependency: crypto must stay a leaf package so it can be unit-tested
// without pulling in configuration.
func ValidateKey(v string) error {
	if v == "" {
		return errors.New("empty key")
	}

	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return fmt.Errorf("key must be base64 encoded: %w", err)
	}

	if len(raw) != KeyLen {
		return fmt.Errorf("key must be %d bytes long, got %d", KeyLen, len(raw))
	}

	return nil
}
