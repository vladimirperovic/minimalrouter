package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestTOTPReplayKeyCanonicalInputs(t *testing.T) {
	// Synthetic key with base32 padding, to exercise decoder aliases too.
	secretBytes := []byte("synthetic totp fixture")
	secret := base32.StdEncoding.EncodeToString(secretBytes)
	lastDigit := strings.IndexByte(secret, '=') - 1
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	// Set one unused low bit in the final digit of this padded encoding.
	aliasDigit := alphabet[strings.IndexByte(alphabet, secret[lastDigit])|1]
	paddingAlias := secret[:lastDigit] + string(aliasDigit) + secret[lastDigit+1:]
	code := computeTOTP(secretBytes, time.Now().Unix()/TOTPPeriod)
	key := TOTPReplayKey(secret, code)
	for _, tt := range []struct {
		name, secret, code string
	}{
		{"original", secret, code},
		{"code-spaces", secret, " " + code + " "},
		{"code-unicode-whitespace", secret, "\t\r\n\u00a0" + code + "\u2003\n"},
		{"secret-lowercase", strings.ToLower(secret), code},
		{"secret-spaces", " " + secret[:8] + " " + secret[8:] + " ", code},
		{"secret-line-breaks", secret[:8] + "\r\n" + secret[8:], code},
		{"secret-unused-bits", paddingAlias, code},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !ValidateTOTP(tt.secret, tt.code) {
				t.Fatal("equivalent TOTP input rejected by validator")
			}
			if TOTPReplayKey(tt.secret, tt.code) != key {
				t.Fatal("equivalent validated input has a different replay key")
			}
		})
	}
	otherCode := "000000"
	if code == otherCode {
		otherCode = "000001"
	}
	if TOTPReplayKey(secret, otherCode) == key {
		t.Fatal("distinct codes share replay key")
	}
	if TOTPReplayKey(base32.StdEncoding.EncodeToString([]byte("different synthetic key")), code) == key {
		t.Fatal("distinct secrets share replay key")
	}
}

func TestTOTPCanonicalizationDoesNotBroadenValidation(t *testing.T) {
	secretBytes := []byte("synthetic totp fixture")
	secret := base32.StdEncoding.EncodeToString(secretBytes)
	code := computeTOTP(secretBytes, time.Now().Unix()/TOTPPeriod)
	for _, tt := range []struct{ secret, code string }{
		{secret, code[:3] + " " + code[3:]},
		{secret, code[:5]},
		{secret, "abcdef"},
		{secret[:8] + "\t" + secret[8:], code},
		{"!" + secret, code},
	} {
		if ValidateTOTP(tt.secret, tt.code) {
			t.Fatal("invalid TOTP input accepted")
		}
	}
}
