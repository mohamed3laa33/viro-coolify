package secrets

import (
	"encoding/base64"
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	for _, pt := range []string{"", "hunter2", "a longer secret value with spaces & symbols !@#$"} {
		enc, err := c.Encrypt(pt)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if enc == pt && pt != "" {
			t.Fatalf("ciphertext equals plaintext for %q", pt)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("encrypted value missing prefix: %q", enc)
		}
		if strings.Contains(enc, pt) && pt != "" {
			t.Fatalf("ciphertext leaks plaintext: %q in %q", pt, enc)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != pt {
			t.Fatalf("round-trip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatalf("expected distinct ciphertexts (random nonce); both = %q", a)
	}
}

func TestDecryptLegacyPlaintextPassThrough(t *testing.T) {
	c := newTestCipher(t)
	// A value without the v1: prefix is legacy plaintext; passed through unchanged.
	got, err := c.Decrypt("legacy-plain")
	if err != nil {
		t.Fatalf("decrypt legacy: %v", err)
	}
	if got != "legacy-plain" {
		t.Fatalf("legacy passthrough = %q", got)
	}
}

func TestNoopCipher(t *testing.T) {
	var c Cipher = NoopCipher{}
	if c.Enabled() {
		t.Fatalf("noop cipher reports enabled")
	}
	enc, _ := c.Encrypt("x")
	if enc != "x" {
		t.Fatalf("noop encrypt = %q", enc)
	}
	dec, _ := c.Decrypt("x")
	if dec != "x" {
		t.Fatalf("noop decrypt = %q", dec)
	}
}

func TestFromConfigEmptyKeyIsNoop(t *testing.T) {
	c, err := FromConfig("")
	if err != nil {
		t.Fatalf("from config empty: %v", err)
	}
	if c.Enabled() {
		t.Fatalf("empty key should yield disabled cipher")
	}
}

func TestFromConfigBase64Key(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := FromConfig(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("from config b64: %v", err)
	}
	if !c.Enabled() {
		t.Fatalf("expected enabled cipher")
	}
	enc, _ := c.Encrypt("v")
	got, _ := c.Decrypt(enc)
	if got != "v" {
		t.Fatalf("round-trip = %q", got)
	}
}

func TestFromConfigBadKey(t *testing.T) {
	if _, err := FromConfig("not-a-valid-32-byte-key"); err == nil {
		t.Fatalf("expected error for malformed key")
	}
}

func TestWrongKeyFailsDecrypt(t *testing.T) {
	c1 := newTestCipher(t)
	enc, _ := c1.Encrypt("secret")

	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(255 - i)
	}
	c2, _ := NewCipher(key2)
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatalf("expected auth failure decrypting with wrong key")
	}
}
