package config

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/argon2"
)

// BackupEncryption provides authenticated encryption for backup snapshots using AES-GCM (NaCl-compatible).
// This is a pure-Go implementation using crypto/aes + crypto/cipher (AES-GCM = NaCl secretbox equivalent).
type BackupEncryption struct {
	key []byte // 32-byte key
}

const (
	backupFormatVersion = 1
	backupArgonTime     = 3
	backupArgonMemory   = 64 * 1024
	backupArgonThreads  = 1
)

var backupAAD = []byte("minimalrouter-backup-v1")

type BackupPayload struct {
	Product       string       `json:"product"`
	FormatVersion int          `json:"format_version"`
	CreatedAt     string       `json:"created_at"`
	Config        SystemConfig `json:"config"`
}

type BackupEnvelope struct {
	Product       string `json:"product"`
	FormatVersion int    `json:"format_version"`
	KDF           string `json:"kdf"`
	ArgonTime     uint32 `json:"argon_time"`
	ArgonMemory   uint32 `json:"argon_memory_kib"`
	ArgonThreads  uint8  `json:"argon_threads"`
	Salt          string `json:"salt"`
	Nonce         string `json:"nonce"`
	Ciphertext    string `json:"ciphertext"`
}

// EncryptConfigBackup creates a password-protected, authenticated backup. The
// passphrase is never persisted and the envelope carries bounded Argon2id
// parameters required for future restoration.
func EncryptConfigBackup(cfg SystemConfig, passphrase string) ([]byte, error) {
	if len(passphrase) < 15 || len(passphrase) > 1024 {
		return nil, errors.New("backup passphrase must contain 15-1024 characters")
	}
	payload, err := json.Marshal(BackupPayload{
		Product:       "Minimal Router OS",
		FormatVersion: backupFormatVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Config:        cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal backup payload: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate backup salt: %w", err)
	}
	key := argon2.IDKey([]byte(passphrase), salt, backupArgonTime, backupArgonMemory, backupArgonThreads, 32)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create backup cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create backup AEAD: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate backup nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, payload, backupAAD)
	envelope := BackupEnvelope{
		Product:       "Minimal Router OS encrypted backup",
		FormatVersion: backupFormatVersion,
		KDF:           "argon2id",
		ArgonTime:     backupArgonTime,
		ArgonMemory:   backupArgonMemory,
		ArgonThreads:  backupArgonThreads,
		Salt:          base64.StdEncoding.EncodeToString(salt),
		Nonce:         base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:    base64.StdEncoding.EncodeToString(ciphertext),
	}
	return json.MarshalIndent(envelope, "", "  ")
}

func DecryptConfigBackup(data []byte, passphrase string) (SystemConfig, error) {
	if len(data) == 0 || len(data) > 16<<20 {
		return SystemConfig{}, errors.New("encrypted backup has an invalid size")
	}
	var envelope BackupEnvelope
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(data), 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return SystemConfig{}, fmt.Errorf("decode encrypted backup: %w", err)
	}
	if envelope.Product != "Minimal Router OS encrypted backup" ||
		envelope.FormatVersion != backupFormatVersion ||
		envelope.KDF != "argon2id" {
		return SystemConfig{}, errors.New("unsupported backup format")
	}
	if envelope.ArgonTime != backupArgonTime ||
		envelope.ArgonMemory != backupArgonMemory ||
		envelope.ArgonThreads != backupArgonThreads {
		return SystemConfig{}, errors.New("unsupported backup KDF profile")
	}
	salt, err := base64.StdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != 16 {
		return SystemConfig{}, errors.New("backup salt is invalid")
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return SystemConfig{}, errors.New("backup nonce is invalid")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) < 16 {
		return SystemConfig{}, errors.New("backup ciphertext is invalid")
	}
	key := argon2.IDKey([]byte(passphrase), salt, envelope.ArgonTime, envelope.ArgonMemory, envelope.ArgonThreads, 32)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return SystemConfig{}, fmt.Errorf("create backup cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return SystemConfig{}, errors.New("backup nonce is invalid")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, backupAAD)
	if err != nil {
		return SystemConfig{}, errors.New("backup authentication failed")
	}
	var payload BackupPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return SystemConfig{}, errors.New("backup payload is invalid")
	}
	if payload.Product != "Minimal Router OS" || payload.FormatVersion != backupFormatVersion {
		return SystemConfig{}, errors.New("backup payload version is unsupported")
	}
	if err := payload.Config.Validate(); err != nil {
		return SystemConfig{}, fmt.Errorf("backup configuration is invalid: %w", err)
	}
	return payload.Config, nil
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
