package crypto

import (
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

	// base64 of a very short byte slice (shorter than nonce)
	_, err = enc.Decrypt("YQ==")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ciphertext too short")
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
	assert.Error(t, err)
}
