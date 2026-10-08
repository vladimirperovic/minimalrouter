// Package recoverybackup carries the v0.2 portable configuration and DNS policy.
// Version-one import stays in config for legacy compatibility. Keeping the new
// payload here preserves the published router-recovery/bootstrap binary ABI.
package recoverybackup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/kdf"
	"golang.org/x/crypto/argon2"
)

type Payload struct {
	Product   string              `json:"product"`
	Version   int                 `json:"version"`
	CreatedAt string              `json:"created_at"`
	Config    config.SystemConfig `json:"config"`
	DNSFilter *dnsfilter.Policy   `json:"dns_filter,omitempty"`
}

const product = "Minimal Router OS encrypted backup"

func cipherFor(password string, salt []byte) (cipher.AEAD, error) {
	if len(password) > 1024 {
		return nil, errors.New("backup password is too long")
	}
	if err := kdf.Acquire(); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	kdf.Release()
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Encrypt(cfg config.SystemConfig, policy *dnsfilter.Policy, password string) ([]byte, error) {
	if len(password) < 12 || len(password) > 1024 {
		return nil, errors.New("dashboard password must contain 12-1024 characters")
	}
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return nil, err
		}
	}
	plaintext, err := json.Marshal(Payload{Product: "Minimal Router OS", Version: 2, CreatedAt: time.Now().UTC().Format(time.RFC3339), Config: cfg, DNSFilter: policy})
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	salt := make([]byte, 16)
	if _, err = io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	aead, err := cipherFor(password, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	envelope := config.BackupEnvelope{Product: product, FormatVersion: 2, KDF: "argon2id", ArgonTime: 3, ArgonMemory: 64 * 1024, ArgonThreads: 1, Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, []byte("minimalrouter-backup-v2")))}
	return json.MarshalIndent(envelope, "", "  ")
}

func Decrypt(raw []byte, password string) (Payload, error) {
	var payload Payload
	if len(raw) == 0 || len(raw) > 16<<20 {
		return payload, errors.New("invalid encrypted backup size")
	}
	var envelope config.BackupEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return payload, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return payload, errors.New("backup contains trailing data")
	}
	if envelope.FormatVersion == 1 {
		cfg, err := config.DecryptConfigBackup(raw, password)
		payload.Config = cfg
		payload.Version = 1
		return payload, err
	}
	if envelope.Product != product || envelope.FormatVersion != 2 || envelope.KDF != "argon2id" || envelope.ArgonTime != 3 || envelope.ArgonMemory != 64*1024 || envelope.ArgonThreads != 1 {
		return payload, errors.New("unsupported backup encryption profile")
	}
	salt, err := base64.StdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != 16 {
		return payload, errors.New("invalid backup salt")
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != 12 {
		return payload, errors.New("invalid backup nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) < 16 {
		return payload, errors.New("invalid backup ciphertext")
	}
	aead, err := cipherFor(password, salt)
	if err != nil {
		return payload, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("minimalrouter-backup-v2"))
	if err != nil {
		return payload, errors.New("backup authentication failed")
	}
	defer clear(plaintext)
	if err = json.Unmarshal(plaintext, &payload); err != nil {
		return payload, errors.New("invalid backup payload")
	}
	if payload.Product != "Minimal Router OS" || payload.Version != 2 {
		return payload, errors.New("unsupported backup payload")
	}
	payload.Config.MigrateLegacyFields()
	if err = payload.Config.Validate(); err != nil {
		return payload, err
	}
	if payload.DNSFilter != nil {
		if err = payload.DNSFilter.Validate(); err != nil {
			return payload, err
		}
	}
	return payload, nil
}
