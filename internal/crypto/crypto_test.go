package crypto

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEncryptor_ValidKey(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)
	assert.NotNil(t, enc)
}

func TestNewEncryptor_InvalidKeyLength(t *testing.T) {
	tests := []struct {
		name    string
		keySize int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
		{"one byte", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keySize)
			enc, err := NewEncryptor(key)
			assert.Error(t, err)
			assert.Nil(t, enc)
			assert.Contains(t, err.Error(), "encryption key must be 32 bytes")
		})
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	tests := []string{
		"hello world",
		"short",
		"a longer string with special chars: !@#$%^&*()",
		"unicode: привет мир 🌍",
	}

	for _, plaintext := range tests {
		t.Run(plaintext, func(t *testing.T) {
			ciphertext, err := enc.Encrypt(plaintext)
			require.NoError(t, err)
			assert.NotEmpty(t, ciphertext)
			assert.NotEqual(t, plaintext, ciphertext)

			decrypted, err := enc.Decrypt(ciphertext)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted)
		})
	}
}

func TestEncrypt_EmptyString(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	ciphertext, err := enc.Encrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", ciphertext)
}

func TestDecrypt_EmptyString(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext, err := enc.Decrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", plaintext)
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	_, err = enc.Decrypt("not-valid-base64!!!")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode base64")
}

func TestDecrypt_CiphertextTooShort(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	// base64 of a very short byte slice (shorter than nonce) — falls
	// through every key with no luck and surfaces ErrDecryptFailed.
	_, err = enc.Decrypt("YQ==")
	assert.ErrorIs(t, err, ErrDecryptFailed)
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	ciphertext, err := enc.Encrypt("secret data")
	require.NoError(t, err)

	// Tamper with the ciphertext
	tampered := []byte(ciphertext)
	tampered[len(tampered)-2] ^= 0xFF
	_, err = enc.Decrypt(string(tampered))
	assert.Error(t, err)
}

func TestEncrypt_DifferentCiphertextsForSamePlaintext(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	ct1, err := enc.Encrypt("same text")
	require.NoError(t, err)

	ct2, err := enc.Encrypt("same text")
	require.NoError(t, err)

	// Due to random nonce, ciphertexts should differ
	assert.NotEqual(t, ct1, ct2)
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := []byte("01234567890123456789012345678901")
	key2 := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")

	enc1, err := NewEncryptor(key1)
	require.NoError(t, err)
	enc2, err := NewEncryptor(key2)
	require.NoError(t, err)

	ciphertext, err := enc1.Encrypt("secret")
	require.NoError(t, err)

	_, err = enc2.Decrypt(ciphertext)
	assert.ErrorIs(t, err, ErrDecryptFailed)
}

func TestKeyRotation_SecondaryKeyDecryptsLegacyValues(t *testing.T) {
	// Operator scenario: an existing deploy has tokens encrypted under
	// keyA. The operator wants to rotate to keyB. They start the bot
	// with keyB as primary and keyA as a secondary — existing tokens
	// continue to decrypt while new writes use keyB.
	keyA := []byte("01234567890123456789012345678901")
	keyB := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")

	oldEnc, err := NewEncryptor(keyA)
	require.NoError(t, err)
	legacy, err := oldEnc.Encrypt("rotated-secret")
	require.NoError(t, err)

	rotated, err := NewEncryptor(keyB)
	require.NoError(t, err)
	require.NoError(t, rotated.AddSecondaryKey(keyA))

	pt, err := rotated.Decrypt(legacy)
	require.NoError(t, err)
	assert.Equal(t, "rotated-secret", pt)

	// New writes must encrypt under keyB; a fresh ciphertext produced
	// here must NOT be decryptable by the old single-key encryptor.
	fresh, err := rotated.Encrypt("new-secret")
	require.NoError(t, err)
	_, err = oldEnc.Decrypt(fresh)
	assert.ErrorIs(t, err, ErrDecryptFailed)
}

func TestAddSecondaryKey_RejectsBadKey(t *testing.T) {
	enc, err := NewEncryptor([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)

	err = enc.AddSecondaryKey([]byte("too-short"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be 32 bytes")
}

func TestDecrypt_LegacyUnversionedBlobStillWorks(t *testing.T) {
	// Backward compat: blobs written before blob versioning landed
	// have no leading 0x01 — they are raw nonce|ciphertext|tag.
	// Construct one by hand and make sure Decrypt accepts it.
	key := []byte("01234567890123456789012345678901")
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	// Use Encrypt to get a valid blob, then strip the version byte.
	versioned, err := enc.Encrypt("pre-migration-value")
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(versioned)
	require.NoError(t, err)
	require.Equal(t, byte(0x01), raw[0])
	legacy := base64.StdEncoding.EncodeToString(raw[1:])

	pt, err := enc.Decrypt(legacy)
	require.NoError(t, err)
	assert.Equal(t, "pre-migration-value", pt)
}
