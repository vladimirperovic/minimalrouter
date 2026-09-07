package main

import "testing"

func TestMigrateConfigRequiresExplicitFileAndConfirmation(t *testing.T) {
	for _, args := range [][]string{nil, {"--file", "candidate.json"}, {"--confirm", "MIGRATE-CONFIG"}, {"--file", "candidate.json", "--confirm", "RESTORE-SNAPSHOT"}, {"--file", "candidate.json", "--confirm", "MIGRATE-CONFIG", "extra"}} {
		if err := migrateConfigCommand(args); err == nil {
			t.Fatalf("accepted incomplete migration invocation: %v", args)
		}
	}
}
