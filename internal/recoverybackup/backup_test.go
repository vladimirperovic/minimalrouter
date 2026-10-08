package recoverybackup

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
)

func TestDashboardPasswordBackupAndLegacyRestore(t *testing.T) {
	const password = "Abcd1234!?xy" // exactly twelve characters
	cfg := config.DefaultConfig()
	cfg.WAN.Password = "test-private-pppoe-value"
	policy := dnsfilter.DefaultPolicy()
	policy.Categories["adult"] = true
	policy.Exceptions = []dnsfilter.Exception{{Domain: "school.example.com", Reason: "School"}}
	raw, err := Encrypt(cfg, &policy, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(cfg.WAN.Password)) || bytes.Contains(raw, []byte("school.example.com")) {
		t.Fatal("backup leaked plaintext settings")
	}
	got, err := Decrypt(raw, password)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.WAN.Password != cfg.WAN.Password || got.DNSFilter == nil || !got.DNSFilter.Categories["adult"] || len(got.DNSFilter.Exceptions) != 1 {
		t.Fatal("backup lost configuration or DNS policy")
	}
	if _, err = Decrypt(raw, "different-dashboard-password"); err == nil {
		t.Fatal("changed password opened older backup")
	}
	if _, err = Decrypt(append(raw, []byte(" {}")...), password); err == nil {
		t.Fatal("trailing data accepted")
	}
	var envelope config.BackupEnvelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.ArgonMemory *= 2
	invalid, _ := json.Marshal(envelope)
	if _, err = Decrypt(invalid, password); err == nil {
		t.Fatal("modified KDF profile accepted")
	}
	legacy, err := config.EncryptConfigBackup(cfg, "old separate backup password")
	if err != nil {
		t.Fatal(err)
	}
	old, err := Decrypt(legacy, "old separate backup password")
	if err != nil || old.DNSFilter != nil || old.Config.WAN.Password != cfg.WAN.Password {
		t.Fatalf("legacy restore: %v", err)
	}
}
