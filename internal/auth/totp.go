package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const (
	// TOTPPeriod is the time step in seconds (standard 30 seconds)
	TOTPPeriod = 30
	// TOTPDigits is the number of digits in the OTP (standard 6)
	TOTPDigits = 6
	// TOTPIssuer is the issuer name for QR codes
	TOTPIssuer = "Minimal Router OS"
)

var ErrTOTPNotConfigured = errors.New("TOTP not configured")

// GenerateTOTPSecret generates a new random TOTP secret (base32 encoded)
func GenerateTOTPSecret() (string, error) {
	secret := make([]byte, 20) // 160 bits
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(secret), nil
}

// ValidateTOTP validates a TOTP code against the secret
func ValidateTOTP(secret, code string) bool {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	code = strings.TrimSpace(code)

	if len(code) != TOTPDigits {
		return false
	}

	decodedSecret, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return false
	}

	counter := time.Now().Unix() / TOTPPeriod

	// Check current, previous, and next window (clock skew tolerance)
	for i := -1; i <= 1; i++ {
		expected := computeTOTP(decodedSecret, counter+int64(i))
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return true
		}
	}

	return false
}

func computeTOTP(secret []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0F
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4])
	truncated &= 0x7FFFFFFF

	mod := truncated % 1000000
	return formatTOTP(mod)
}

func formatTOTP(num uint32) string {
	return sprintf("%06d", num)
}

func sprintf(format string, a ...interface{}) string {
	return strings.TrimLeft(sprintfInternal(format, a...), " ")
}

func sprintfInternal(format string, a ...interface{}) string {
	// Simple sprintf replacement for %06d
	if format == "%06d" {
		return padLeft(int(a[0].(uint32)), 6)
	}
	return ""
}

func padLeft(n int, width int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// GetTOTPSecret retrieves the TOTP secret from the store
func GetTOTPSecret(store *config.SQLiteStore) (string, error) {
	return store.GetAdminTOTPSecret()
}

// SetTOTPSecret stores the TOTP secret
func SetTOTPSecret(store *config.SQLiteStore, secret string) error {
	return store.SetAdminTOTPSecret(secret)
}

// ClearTOTPSecret removes the TOTP secret
func ClearTOTPSecret(store *config.SQLiteStore) error {
	return store.ClearAdminTOTPSecret()
}

// BuildTOTPURI builds the otpauth:// URI for QR code generation
func BuildTOTPURI(username, secret string) string {
	return "otpauth://totp/" + TOTPIssuer + ":" + username + "?secret=" + secret + "&issuer=" + TOTPIssuer + "&algorithm=SHA1&digits=6&period=30"
}

// SetupTOTP enables TOTP for admin, stores secret in SQLite.
func SetupTOTP(store *config.SQLiteStore) (secret string, uri string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	if err := store.SetAdminTOTPSecret(secret); err != nil {
		return "", "", err
	}
	uri = BuildTOTPURI("admin", secret)
	return secret, uri, nil
}

// DisableTOTP disables TOTP for admin, clears secret from SQLite.
func DisableTOTP(store *config.SQLiteStore) error {
	return store.ClearAdminTOTPSecret()
}

// VerifyTOTP verifies a TOTP code against stored secret.
func VerifyTOTP(store *config.SQLiteStore, code string) (bool, error) {
	secret, err := store.GetAdminTOTPSecret()
	if err != nil {
		return false, err
	}
	if secret == "" {
		return false, ErrTOTPNotConfigured // TOTP not enabled
	}
	return ValidateTOTP(secret, code), nil
}