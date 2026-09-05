package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/vladimirperovic/minimalrouter/internal/kdf"
)

// Argon2id parameters following SECURITY.md section 5:
// Memory: 64 MiB (65536 KiB), Iterations: 3, Parallelism: 2, KeyLen: 32, SaltLen: 16
const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLen      = 32
	argonSaltLen     = 16
)

var (
	ErrPasswordTooShort = errors.New("password must be at least 12 characters long")
	ErrPasswordTooLong  = errors.New("password must be at most 1024 bytes")
	ErrInvalidHash      = errors.New("the encoded hash is not in the correct format")
	ErrIncompatibleVer  = errors.New("incompatible version of argon2")
)

// HashPassword hashes a plain text password using Argon2id per SECURITY.md §5.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < 12 {
		return "", ErrPasswordTooShort
	}
	if len(password) > 1024 {
		return "", ErrPasswordTooLong
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate random salt: %w", err)
	}

	if err := kdf.Acquire(); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	kdf.Release()

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encodedHash := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism, b64Salt, b64Hash)

	return encodedHash, nil
}

// VerifyPassword performs constant-time comparison of a password against an Argon2id hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	if len(password) > 1024 || len(encodedHash) > 512 {
		return false, ErrInvalidHash
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrIncompatibleVer
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, ErrInvalidHash
	}
	if memory < 16*1024 || memory > 256*1024 ||
		iterations < 1 || iterations > 10 ||
		parallelism < 1 || parallelism > 8 {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	if len(salt) < 16 || len(salt) > 64 {
		return false, ErrInvalidHash
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}
	if len(hash) < 16 || len(hash) > 64 {
		return false, ErrInvalidHash
	}

	if err := kdf.Acquire(); err != nil {
		return false, err
	}
	otherHash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(hash)))
	kdf.Release()

	if subtle.ConstantTimeCompare(hash, otherHash) == 1 {
		return true, nil
	}

	return false, nil
}
