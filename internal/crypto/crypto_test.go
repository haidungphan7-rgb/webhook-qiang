package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()

	c, err := NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestNewCipher_KeyValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewCipher(nil); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("empty key must return ErrNoMasterKey, got %v", err)
	}

	if _, err := NewCipher(make([]byte, 31)); err == nil {
		t.Fatal("a 31 byte key must be rejected")
	}

	if _, err := NewCipher(make([]byte, 33)); err == nil {
		t.Fatal("a 33 byte key must be rejected")
	}
}

func TestCipher_RoundTrip(t *testing.T) {
	t.Parallel()

	c := testCipher(t)

	id := uuid.New()

	blob, err := c.Encrypt(id, []byte("Bearer secret"))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(blob, []byte("secret")) {
		t.Fatal("ciphertext must not contain the plaintext")
	}

	plain, err := c.Decrypt(id, blob)
	if err != nil {
		t.Fatal(err)
	}

	if string(plain) != "Bearer secret" {
		t.Fatalf("got %q", plain)
	}
}

// TestCipher_CiphertextIsBoundToInbox proves the inbox id is used as additional data: a
// blob moved to another row must not decrypt.
func TestCipher_CiphertextIsBoundToInbox(t *testing.T) {
	t.Parallel()

	c := testCipher(t)

	idA, idB := uuid.New(), uuid.New()

	blob, err := c.Encrypt(idA, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = c.Decrypt(idB, blob); err == nil {
		t.Fatal("a ciphertext must not be decryptable under another inbox key")
	}
}

func TestCipher_NonceIsRandom(t *testing.T) {
	t.Parallel()

	c := testCipher(t)

	id := uuid.New()

	a, err := c.Encrypt(id, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := c.Encrypt(id, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(a, b) {
		t.Fatal("encrypting the same plaintext twice must not produce the same blob")
	}
}

func TestCipher_TamperedBlobFails(t *testing.T) {
	t.Parallel()

	c := testCipher(t)

	id := uuid.New()

	blob, err := c.Encrypt(id, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	blob[len(blob)-1] ^= 0xff

	if _, err = c.Decrypt(id, blob); err == nil {
		t.Fatal("a tampered blob must not decrypt")
	}
}

func TestCipher_MalformedBlob(t *testing.T) {
	t.Parallel()

	c := testCipher(t)

	if _, err := c.Decrypt(uuid.New(), []byte{0x01, 0x02}); !errors.Is(err, ErrMalformedBlob) {
		t.Fatalf("expected ErrMalformedBlob, got %v", err)
	}
}

func TestSignVerify(t *testing.T) {
	t.Parallel()

	secret := []byte("s3cr3t")
	body := []byte(`{"a":1}`)

	mac := Sign(secret, body)

	if !Verify(secret, body, mac) {
		t.Fatal("a valid signature must verify")
	}

	if !Verify(secret, body, "sha256="+mac) {
		t.Fatal("the sha256= prefix must be accepted")
	}

	if Verify(secret, []byte(`{"a":2}`), mac) {
		t.Fatal("a modified body must not verify")
	}

	if Verify([]byte("other"), body, mac) {
		t.Fatal("a wrong secret must not verify")
	}

	for _, bad := range []string{"", "xyz", "deadbeef"} {
		if Verify(secret, body, bad) {
			t.Fatalf("%q must not verify", bad)
		}
	}
}

func TestRandomBytes(t *testing.T) {
	t.Parallel()

	a, err := RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}

	b, err := RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}

	if len(a) != 32 || bytes.Equal(a, b) {
		t.Fatal("random bytes must be unique and of the requested length")
	}

	if base64.StdEncoding.EncodeToString(a) == "" {
		t.Fatal("unexpected empty value")
	}
}
