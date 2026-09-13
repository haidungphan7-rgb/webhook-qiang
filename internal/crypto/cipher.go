// Package crypto provides the two primitives the application needs:
//
//  1. per-inbox authenticated encryption for secrets at rest (signing keys, sensitive
//     header values) - HKDF-SHA256 derives a per-inbox key from the master key, so a
//     leaked ciphertext of one inbox does not endanger the others;
//  2. HMAC-SHA256 signing/verification for inbound webhook signatures and for
//     re-signing a request during replay.
//
// It is a leaf package on purpose: no configuration, no storage, no logging. That keeps
// it trivially unit-testable and safe to call from anywhere.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// blobLayout = version(1) || nonce(12) || ciphertext+tag.
const (
	blobVersion byte = 0x01
	nonceSize   int  = 12
	keySize     int  = 32 // AES-256
)

var (
	// ErrMalformedBlob is returned when the stored blob has an unexpected shape.
	ErrMalformedBlob = errors.New("malformed encrypted blob")
	// ErrNoMasterKey is returned when encryption is requested without a configured key.
	ErrNoMasterKey = errors.New("no master encryption key configured")
)

// Cipher encrypts and decrypts values with a key derived per inbox.
type Cipher struct{ master []byte }

// NewCipher creates a cipher from a 32 byte master key. Returns ErrNoMasterKey for an
// empty key - callers treat "no cipher" as "mask instead of encrypt".
func NewCipher(master []byte) (*Cipher, error) {
	if len(master) == 0 {
		return nil, ErrNoMasterKey
	}

	if len(master) != keySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", keySize, len(master))
	}

	return &Cipher{master: master}, nil
}

// derive builds a per-inbox key: HKDF-SHA256(ikm=master, salt=nil, info=inboxID).
// Using the inbox ID as `info` binds the ciphertext to its owner and prevents a
// ciphertext from being moved between rows unnoticed.
func (c *Cipher) derive(inboxID [16]byte) []byte {
	var (
		prk    = hmac.New(sha256.New, c.master)
		info   = inboxID[:]
		out    = make([]byte, keySize)
		blocks = (keySize + sha256.Size - 1) / sha256.Size
	)

	_, _ = prk.Write(info) // hash.Hash.Write never fails
	prkSum := prk.Sum(nil)

	var (
		t    = make([]byte, 0, blocks*sha256.Size)
		prev []byte
	)

	for i := 0; i < blocks; i++ {
		h := hmac.New(sha256.New, prkSum)
		_, _ = h.Write(prev)
		_, _ = h.Write(info)
		_, _ = h.Write([]byte{byte(i + 1)})
		prev = h.Sum(nil)
		t = append(t, prev...)
	}

	copy(out, t[:keySize])

	return out
}

// Encrypt seals plaintext for the given inbox.
func (c *Cipher) Encrypt(inboxID [16]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.derive(inboxID))
	if err != nil {
		return nil, fmt.Errorf("cannot create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cannot create gcm: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cannot read random nonce: %w", err)
	}

	var (
		ct   = gcm.Seal(nil, nonce, plaintext, inboxID[:]) // inbox id is used as additional data
		blob = make([]byte, 0, 1+nonceSize+len(ct))
	)

	blob = append(blob, blobVersion)
	blob = append(blob, nonce...)
	blob = append(blob, ct...)

	return blob, nil
}

// Decrypt opens a blob produced by Encrypt. It fails if the blob was created for a
// different inbox (additional data mismatch) or with a different key.
func (c *Cipher) Decrypt(inboxID [16]byte, blob []byte) ([]byte, error) {
	if len(blob) < 1+nonceSize+aes.BlockSize {
		return nil, ErrMalformedBlob
	}

	if blob[0] != blobVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedBlob, blob[0])
	}

	block, err := aes.NewCipher(c.derive(inboxID))
	if err != nil {
		return nil, fmt.Errorf("cannot create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cannot create gcm: %w", err)
	}

	nonce, ct := blob[1:1+nonceSize], blob[1+nonceSize:]

	pt, err := gcm.Open(nil, nonce, ct, inboxID[:])
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt: %w", err)
	}

	return pt, nil
}

// RandomBytes returns n cryptographically random bytes (used for inbox tokens and
// signing secrets).
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("cannot read random bytes: %w", err)
	}

	return b, nil
}
