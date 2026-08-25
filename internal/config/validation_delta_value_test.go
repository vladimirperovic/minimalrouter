package config

import "testing"

// Delta validation excuses a stale fault so an appliance upgraded in place is
// not locked out of every later edit. It must excuse only the value that is
// already stored: suppressing by message alone let one stored fault permanently
// license every other bad value in that field that produced the same text.

func TestStaleFaultDoesNotLicenseADifferentBadValue(t *testing.T) {
	stored := DefaultConfig()
	stored.WAN.MTU = 900
	if err := stored.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}

	untouched := stored
	untouched.Accounting.Enabled = true
	if err := untouched.ValidateChangesFrom(&stored); err != nil {
		t.Fatalf("an unrelated edit beside an untouched stale fault must pass: %v", err)
	}

	swapped := stored
	swapped.WAN.MTU = 99999
	if err := swapped.ValidateChangesFrom(&stored); err == nil {
		t.Fatal("editing the stale field to another invalid value must be rejected")
	}

	repaired := stored
	repaired.WAN.MTU = 1492
	if err := repaired.ValidateChangesFrom(&stored); err != nil {
		t.Fatalf("repairing the stale field must pass: %v", err)
	}
}

// The same rule has to hold for an indexed field path.
func TestStaleFaultIsScopedToTheStoredArrayElement(t *testing.T) {
	stored := DefaultConfig()
	stored.Firewall.CustomRules = []FirewallRule{{
		Name: "bad name!!", Enabled: true, Action: "deny",
		Direction: "forward", Protocol: "tcp", DstPort: 445,
	}}
	if err := stored.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}

	untouched := stored
	untouched.Accounting.Enabled = true
	if err := untouched.ValidateChangesFrom(&stored); err != nil {
		t.Fatalf("an unrelated edit must pass: %v", err)
	}

	renamed := stored
	renamed.Firewall.CustomRules = []FirewallRule{{
		Name: "other bad!!", Enabled: true, Action: "deny",
		Direction: "forward", Protocol: "tcp", DstPort: 445,
	}}
	if err := renamed.ValidateChangesFrom(&stored); err == nil {
		t.Fatal("renaming the stale rule to another invalid name must be rejected")
	}
}

// An omitempty secret is absent from the JSON projection rather than present
// and empty; "absent on both sides" is still unchanged.
func TestStaleFaultSurvivesAnOmitemptyField(t *testing.T) {
	stored := DefaultConfig()
	stored.WAN.Enabled = true
	stored.WAN.Username = "isp-user"
	stored.WAN.Password = ""
	if err := stored.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}

	untouched := stored
	untouched.Accounting.Enabled = true
	if err := untouched.ValidateChangesFrom(&stored); err != nil {
		t.Fatalf("an untouched empty secret must stay excused: %v", err)
	}
}
