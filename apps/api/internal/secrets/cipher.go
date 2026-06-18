// Package secrets provides AES-256-GCM encryption-at-rest for tenant secret
// values (app env marked secret, and any future at-rest secret material).
//
// A Cipher is constructed from a 32-byte key (loaded from
// VORTEX_SECRET_ENCRYPTION_KEY). When no key is configured the package returns a
// pass-through NoopCipher so local/dev keeps working WITHOUT encryption — the
// caller logs a warning, and the system never panics for a missing key.
//
// On-the-wire format of an encrypted value is: "v1:" + base64(nonce || ciphertext)
// where ciphertext already includes the GCM auth tag. The "v1:" prefix lets the
// decrypt path recognize encrypted-at-rest values and distinguish them from
// legacy plaintext (which it passes through unchanged for a smooth migration).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// encPrefix tags an encrypted-at-rest value so the decrypt path can recognize it
// (and pass legacy plaintext through unchanged during migration).
const encPrefix = "v1:"

// ErrKeyLength is returned when a configured key is not exactly 32 bytes.
var ErrKeyLength = errors.New("secrets: encryption key must be 32 bytes")

// Cipher encrypts and decrypts secret values at rest.
type Cipher interface {
	// Encrypt returns the at-rest representation of a plaintext value.
	Encrypt(plaintext string) (string, error)
	// Decrypt returns the plaintext for an at-rest value. A value without the
	// encrypted-at-rest prefix is treated as legacy plaintext and returned as-is.
	Decrypt(stored string) (string, error)
	// Enabled reports whether real encryption is active (false for the no-op).
	Enabled() bool
}

// gcmCipher is the AES-256-GCM Cipher.
type gcmCipher struct {
	aead cipher.AEAD
}

// NoopCipher is the pass-through Cipher used when no key is configured (dev). It
// stores values as plaintext. Decrypt still strips a "v1:" prefix it cannot
// otherwise produce, so it is safe even on mixed data.
type NoopCipher struct{}

func (NoopCipher) Encrypt(plaintext string) (string, error) { return plaintext, nil }
func (NoopCipher) Decrypt(stored string) (string, error) {
	// A no-op cipher never produced a v1: value; pass everything through.
	return stored, nil
}
func (NoopCipher) Enabled() bool { return false }

// DecodeKey parses a 32-byte key from a base64 (standard or URL, padded or not)
// or hex string. It returns ErrKeyLength when the decoded key is not 32 bytes.
func DecodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("secrets: empty key")
	}
	// Try hex first (64 hex chars), then base64 variants.
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, ErrKeyLength
			}
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) == 32 {
				return b, nil
			}
		}
	}
	// Last resort: a raw 32-byte string.
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, ErrKeyLength
}

// NewCipher builds an AES-256-GCM Cipher from a 32-byte key.
func NewCipher(key []byte) (Cipher, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return &gcmCipher{aead: aead}, nil
}

// FromConfig builds a Cipher from the configured key string. An empty key yields
// a NoopCipher (enabled=false) so the caller can log a warning and keep going in
// dev. A non-empty but malformed key is a hard error (operator misconfiguration).
func FromConfig(key string) (Cipher, error) {
	if strings.TrimSpace(key) == "" {
		return NoopCipher{}, nil
	}
	raw, err := DecodeKey(key)
	if err != nil {
		return nil, err
	}
	return NewCipher(raw)
}

func (c *gcmCipher) Enabled() bool { return true }

func (c *gcmCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return encPrefix + base64.StdEncoding.EncodeToString(out), nil
}

func (c *gcmCipher) Decrypt(stored string) (string, error) {
	// Legacy plaintext (written before encryption was enabled) lacks the prefix;
	// return it unchanged so a key roll-in doesn't break existing values.
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", fmt.Errorf("secrets: decode: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: open: %w", err)
	}
	return string(pt), nil
}

// IsEncrypted reports whether a stored value carries the encrypted-at-rest prefix.
func IsEncrypted(stored string) bool { return strings.HasPrefix(stored, encPrefix) }
