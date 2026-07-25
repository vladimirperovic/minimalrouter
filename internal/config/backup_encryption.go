package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// BackupEncryption provides authenticated encryption for backup snapshots using AES-GCM (NaCl-compatible).
// This is a pure-Go implementation using crypto/aes + crypto/cipher (AES-GCM = NaCl secretbox equivalent).
type BackupEncryption struct {
	key []byte // 32-byte key
}

// NewBackupEncryption creates a new backup encryption instance from a 32-byte key.
func NewBackupEncryption(key []byte) (*BackupEncryption, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	return &BackupEncryption{key: key}, nil
}

// GenerateBackupKey generates a new random 32-byte encryption key.
func GenerateBackupKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// EncryptSnapshot encrypts a snapshot config to base64-encoded ciphertext.
// Format: nonce(12) + ciphertext + tag (AES-GCM appends tag to ciphertext)
// Returns base64 string suitable for storage/transfer.
func (be *BackupEncryption) EncryptSnapshot(snap Snapshot) (string, error) {
	data, err := json.Marshal(snap)
	if err != nil {
		return "", fmt.Errorf("marshal snapshot: %w", err)
	}

	block, err := aes.NewCipher(be.key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSnapshot decrypts a base64-encoded encrypted snapshot.
func (be *BackupEncryption) DecryptSnapshot(encoded string) (Snapshot, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode base64: %w", err)
	}

	block, err := aes.NewCipher(be.key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Snapshot{}, fmt.Errorf("new GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return Snapshot{}, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("decrypt: %w", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(plaintext, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return snap, nil
}

// KeyToBase64 encodes the encryption key to base64 for storage/backup.
func (be *BackupEncryption) KeyToBase64() string {
	return base64.StdEncoding.EncodeToString(be.key)
}

// KeyFromBase64 creates a BackupEncryption from a base64-encoded key.
func KeyFromBase64(encoded string) (*BackupEncryption, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return NewBackupEncryption(key)
}