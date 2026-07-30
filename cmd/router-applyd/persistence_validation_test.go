package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func validTransactionRecordForTest() transactionRecord {
	return transactionRecord{
		ID:         "tx-valid-record",
		ConfigHash: hex.EncodeToString(make([]byte, sha256.Size)),
		Response: apply.ApplyResponse{
			ID: "tx-valid-record", Success: true, Verified: true,
		},
		CompletedAt: time.Now(),
	}
}

func TestValidateTransactionRecordRejectsSemanticallyEmptyJSON(t *testing.T) {
	if err := validateTransactionRecord(transactionRecord{}); err == nil {
		t.Fatal("empty transaction record was accepted")
	}
}

func TestValidateTransactionRecordChecksFingerprintResponseAndTime(t *testing.T) {
	valid := validTransactionRecordForTest()
	if err := validateTransactionRecord(valid); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*transactionRecord)
	}{
		{name: "bad fingerprint", mutate: func(r *transactionRecord) { r.ConfigHash = "xyz" }},
		{name: "missing completion", mutate: func(r *transactionRecord) { r.CompletedAt = time.Time{} }},
		{name: "response ID mismatch", mutate: func(r *transactionRecord) { r.Response.ID = "other" }},
		{name: "contradictory response", mutate: func(r *transactionRecord) { r.Response = apply.ApplyResponse{ID: r.ID, Success: true} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)
			if err := validateTransactionRecord(record); err == nil {
				t.Fatal("invalid transaction record was accepted")
			}
		})
	}
}

func TestValidatePendingConfirmationBindsHashToCompleteConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	hash, err := hashConfig(cfg)
	if err != nil {
		t.Fatalf("hash config: %v", err)
	}
	pending := pendingConfirmation{ConfigHash: hash, Config: cfg}
	if err := validatePendingConfirmation(pending); err != nil {
		t.Fatalf("valid pending state rejected: %v", err)
	}
	pending.Config.System.Hostname = "tampered-after-hash"
	if err := validatePendingConfirmation(pending); err == nil {
		t.Fatal("tampered pending configuration was accepted")
	}
}

func TestSaveLastGoodRejectsInvalidConfigBeforeWrite(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LAN.CIDR = "not-a-cidr"
	if err := cfg.Validate(); err == nil {
		t.Fatal("test configuration unexpectedly valid")
	}
}
