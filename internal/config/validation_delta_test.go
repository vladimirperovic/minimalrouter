package config

import "testing"

// An appliance upgraded from an older release can be carrying values a newer
// rule rejects. The dashboard sends the whole configuration on every write, so
// without delta validation one stale field makes every later edit impossible.

func upgradedApplianceConfig(t *testing.T) SystemConfig {
	t.Helper()
	cfg := DefaultConfig()
	// PPPoE marked on by an older release that did not demand a stored secret.
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}
	return cfg
}

func TestValidateChangesFromAllowsEditsBesideAStaleFault(t *testing.T) {
	stored := upgradedApplianceConfig(t)

	next := stored
	next.Accounting.Enabled = true

	if err := next.ValidateChangesFrom(&stored); err != nil {
		t.Fatalf("an unrelated toggle must not be blocked by a pre-existing fault: %v", err)
	}
}

func TestValidateChangesFromStillRejectsWhatTheChangeBreaks(t *testing.T) {
	stored := upgradedApplianceConfig(t)

	next := stored
	next.LAN.IPAddress = "not-an-address"

	err := next.ValidateChangesFrom(&stored)
	if err == nil {
		t.Fatal("a fault introduced by the change must still be rejected")
	}
	errs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	for _, item := range errs {
		if item.Field == "wan.password" {
			t.Error("the pre-existing wan.password fault must not be reported as introduced")
		}
	}
	var sawLAN bool
	for _, item := range errs {
		if item.Field == "lan.ip_address" {
			sawLAN = true
		}
	}
	if !sawLAN {
		t.Errorf("expected the introduced lan.ip_address fault, got %v", errs)
	}
}

func TestValidateChangesFromIsPlainValidationOnAHealthyStoredConfig(t *testing.T) {
	stored := DefaultConfig()
	if err := stored.Validate(); err != nil {
		t.Fatalf("the default configuration must be valid: %v", err)
	}

	next := stored
	next.LAN.IPAddress = "not-an-address"

	if err := next.ValidateChangesFrom(&stored); err == nil {
		t.Fatal("nothing is excused when the stored configuration is clean")
	}
}

func TestValidateChangesFromWithoutPreviousBehavesLikeValidate(t *testing.T) {
	cfg := upgradedApplianceConfig(t)

	if err := cfg.ValidateChangesFrom(nil); err == nil {
		t.Fatal("a nil previous configuration must fall back to full validation")
	}
}

func TestSquidPasswordAcceptsEightCharacters(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SquidProxy.Enabled = true
	cfg.SquidProxy.Username = "proxyadmin"
	cfg.SquidProxy.Password = "12345678"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("an eight character proxy password must be accepted: %v", err)
	}

	cfg.SquidProxy.Password = "1234567"
	if err := cfg.Validate(); err == nil {
		t.Fatal("seven characters must still be rejected")
	}
}
