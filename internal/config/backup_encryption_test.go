package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestEncryptedConfigBackupRoundTripAndTamperRejection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = "isp-secret"

	encrypted, err := EncryptConfigBackup(cfg, "correct horse battery staple")
	if err != nil {
		t.Fatalf("EncryptConfigBackup failed: %v", err)
	}
	if bytes.Contains(encrypted, []byte("isp-secret")) {
		t.Fatal("encrypted backup leaked a plaintext secret")
	}
	restored, err := DecryptConfigBackup(encrypted, "correct horse battery staple")
	if err != nil {
		t.Fatalf("DecryptConfigBackup failed: %v", err)
	}
	if restored.WAN.Password != cfg.WAN.Password {
		t.Fatal("restored backup lost the PPPoE secret")
	}
	if _, err := DecryptConfigBackup(encrypted, "wrong password value"); err == nil {
		t.Fatal("wrong backup passphrase was accepted")
	}

	var envelope BackupEnvelope
	if err := json.Unmarshal(encrypted, &envelope); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)/2] ^= 0x80
	envelope.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptConfigBackup(tampered, "correct horse battery staple"); err == nil {
		t.Fatal("tampered backup was accepted")
	}
}

func TestDecryptBackupRejectsUnboundedArgon2Parameters(t *testing.T) {
	// A crafted backup must never be able to force the importer into a
	// resource-exhausting KDF run. The envelope bounds are the import side of
	// the security boundary.
	valid, err := EncryptConfigBackup(DefaultConfig(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	var envelope BackupEnvelope
	if err := json.Unmarshal(valid, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []struct {
		name   string
		change func(*BackupEnvelope)
	}{
		{"memory bomb", func(e *BackupEnvelope) { e.ArgonMemory = 4 * 1024 * 1024 }},
		{"thread bomb", func(e *BackupEnvelope) { e.ArgonThreads = 64 }},
		{"time bomb", func(e *BackupEnvelope) { e.ArgonTime = 1000 }},
		{"zero cost", func(e *BackupEnvelope) { e.ArgonTime, e.ArgonMemory, e.ArgonThreads = 0, 0, 0 }},
	} {
		attack := envelope
		mutate.change(&attack)
		raw, err := json.Marshal(attack)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecryptConfigBackup(raw, "correct horse battery staple"); err == nil {
			t.Fatalf("backup with %s was accepted", mutate.name)
		}
	}
}

func TestDecryptBackupRejectsUnknownBoundedArgon2Profile(t *testing.T) {
	valid, err := EncryptConfigBackup(DefaultConfig(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	var envelope BackupEnvelope
	if err := json.Unmarshal(valid, &envelope); err != nil {
		t.Fatal(err)
	}

	// These values are within the old generic safety bounds, but are not the
	// known v1 profile. Reject them before Argon2 runs so future profiles cannot
	// silently consume more memory than the appliance budget allows.
	for _, mutate := range []struct {
		name   string
		change func(*BackupEnvelope)
	}{
		{"higher memory", func(e *BackupEnvelope) { e.ArgonMemory = 128 * 1024 }},
		{"extra thread", func(e *BackupEnvelope) { e.ArgonThreads = 2 }},
		{"different time cost", func(e *BackupEnvelope) { e.ArgonTime = 4 }},
	} {
		attack := envelope
		mutate.change(&attack)
		raw, err := json.Marshal(attack)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecryptConfigBackup(raw, "correct horse battery staple"); err == nil {
			t.Fatalf("backup with %s was accepted", mutate.name)
		}
	}
}
