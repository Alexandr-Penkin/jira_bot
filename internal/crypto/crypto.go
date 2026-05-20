package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
)

// blobVersion is the leading byte of an encrypted blob produced by this
// package. Older code wrote raw "nonce|ciphertext" with no version tag,
// so Decrypt also accepts that legacy layout — see decryptPrimary for
// the migration path.
const (
	blobVersionV1 byte = 0x01
)

// Encryptor provides AES-GCM encryption and decryption for sensitive
// data. It supports an ordered list of secondary keys so operators can
// rotate ENCRYPTION_KEY without rewriting every existing ciphertext:
// new values are encrypted under the primary key, decryption tries the
// primary first and then each secondary in order.
type Encryptor struct {
	primary    cipher.AEAD
	secondary  []cipher.AEAD
	nonceSize  int
	primaryRaw []byte // kept for equality checks in tests, never logged
}

// NewEncryptorFromHex constructs an Encryptor from hex-encoded keys.
// primaryHex is required (64 hex chars = 32 bytes). previousHex is
// optional; when non-empty it is registered as a secondary key so the
// caller can rotate ENCRYPTION_KEY without invalidating existing
// ciphertext. Centralised here so every *-svc binary does the same
// decoding instead of open-coding hex.DecodeString + AddSecondaryKey.
func NewEncryptorFromHex(primaryHex, previousHex string) (*Encryptor, error) {
	primary, err := hex.DecodeString(primaryHex)
	if err != nil {
		return nil, fmt.Errorf("decode primary key: %w", err)
	}
	enc, err := NewEncryptor(primary)
	if err != nil {
		return nil, err
	}
	if previousHex != "" {
		prev, err := hex.DecodeString(previousHex)
		if err != nil {
			return nil, fmt.Errorf("decode previous key: %w", err)
		}
		if err := enc.AddSecondaryKey(prev); err != nil {
			return nil, fmt.Errorf("register previous key: %w", err)
		}
	}
	return enc, nil
}

// NewEncryptor creates an Encryptor from a 32-byte key. key must be
// exactly 32 bytes for AES-256.
func NewEncryptor(key []byte) (*Encryptor, error) {
	primary, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &Encryptor{
		primary:    primary,
		nonceSize:  primary.NonceSize(),
		primaryRaw: slices.Clone(key),
	}, nil
}

// AddSecondaryKey registers an additional 32-byte key used as a Decrypt
// fallback. Call once per legacy key during rotation; new Encrypt calls
// always use the primary key. Returns an error for malformed keys so a
// startup misconfiguration fails loudly instead of silently degrading
// to single-key behaviour.
func (e *Encryptor) AddSecondaryKey(key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	if gcm.NonceSize() != e.nonceSize {
		return fmt.Errorf("secondary key nonce size %d does not match primary %d", gcm.NonceSize(), e.nonceSize)
	}
	e.secondary = append(e.secondary, gcm)
	return nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return gcm, nil
}

// Encrypt encrypts plaintext under the primary key and returns a
// base64-encoded blob prefixed with blobVersionV1. Empty input returns
// empty output.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	nonce := make([]byte, e.nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Layout: 0x01 | nonce | ciphertext|tag
	out := make([]byte, 0, 1+e.nonceSize+len(plaintext)+e.primary.Overhead())
	out = append(out, blobVersionV1)
	out = append(out, nonce...)
	out = e.primary.Seal(out, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// ErrDecryptFailed wraps the underlying GCM error so callers can tell
// "wrong key / tampered ciphertext" apart from "malformed input". A
// rotation that loses access to all relevant keys will surface as this
// error rather than the previous silent empty-string return.
var ErrDecryptFailed = errors.New("crypto: decryption failed")

// Decrypt parses a blob produced by Encrypt (or, for backward compat,
// the pre-versioning raw layout) and returns the plaintext. Tries the
// primary key first, then each registered secondary key in order.
// Empty input returns empty output.
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	// Detect format. v1 starts with 0x01; legacy starts with the
	// nonce directly. The probability that a legacy blob's first
	// nonce byte happens to be 0x01 is non-zero but bounded — when
	// it collides the primary-key Open will fail and we fall through
	// to the legacy interpretation.
	if len(raw) >= 1 && raw[0] == blobVersionV1 {
		if plaintext, ok := e.tryAllKeys(raw[1:]); ok {
			return plaintext, nil
		}
		// Fall through to legacy interpretation in case the leading
		// byte is a coincidence and the blob is actually raw nonce.
	}

	if plaintext, ok := e.tryAllKeys(raw); ok {
		return plaintext, nil
	}

	return "", ErrDecryptFailed
}

// tryAllKeys attempts decryption against the primary key, then each
// secondary in order. Returns (plaintext, true) on the first success.
func (e *Encryptor) tryAllKeys(nonceAndCT []byte) (string, bool) {
	if len(nonceAndCT) < e.nonceSize {
		return "", false
	}
	nonce, ct := nonceAndCT[:e.nonceSize], nonceAndCT[e.nonceSize:]

	if plaintext, err := e.primary.Open(nil, nonce, ct, nil); err == nil {
		return string(plaintext), true
	}
	for _, sec := range e.secondary {
		if plaintext, err := sec.Open(nil, nonce, ct, nil); err == nil {
			return string(plaintext), true
		}
	}
	return "", false
}
