// Package secrets provides AES-GCM at-rest encryption for credentials and a
// Redact helper that masks secrets in logs/audit. Spec:
// openspec/.../specs/station-management/spec.md (Requirements: 凭据静态加密 / 日志脱敏).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// DeriveKey derives a 32-byte AES-256 key from a passphrase via SHA-256.
func DeriveKey(passphrase string) []byte {
	h := sha256.Sum256([]byte(passphrase))
	return h[:]
}

// Encrypt produces AES-GCM ciphertext + nonce for plaintext.
func Encrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt reverses Encrypt. Returns an error on key/nonce/tamper mismatch.
func Decrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid nonce size")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// Redact masks a secret for logging, keeping only a short type prefix.
// "sk-abc123" → "sk-***"; "ab" → "***"; "" → "".
func Redact(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 3 {
		return "***"
	}
	return s[:3] + "***"
}
