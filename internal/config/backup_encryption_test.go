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
