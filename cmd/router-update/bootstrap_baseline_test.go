package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packaging/alpine/bootstrap-baseline.json records which release the bootstrap
// binaries are byte-identical to, and scripts/ci/bootstrap-compatibility.sh
// enforces that claim. Both are meaningless if they describe a different set of
// binaries than the activation check actually compares, so tie them together.
func TestBootstrapBaselineDescribesTheVerifiedBinaries(t *testing.T) {
	path := filepath.Join("..", "..", "packaging", "alpine", "bootstrap-baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bootstrap baseline: %v", err)
	}
	var baseline struct {
		BaselineRef       string   `json:"bootstrap_baseline_ref"`
		Commands          []string `json:"bootstrap_commands"`
		Architectures     []string `json:"architectures"`
		AcknowledgedFor   string   `json:"bootstrap_change_acknowledged_version"`
		AcknowledgedWhy   string   `json:"bootstrap_change_reason"`
		RequiresInstaller string   `json:"requires_full_installer_from"`
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("bootstrap baseline is not valid JSON: %v", err)
	}
	if baseline.BaselineRef == "" {
		t.Fatal("bootstrap_baseline_ref must name the release the bootstrap bytes come from")
	}
	if baseline.AcknowledgedFor != "" && strings.TrimSpace(baseline.AcknowledgedWhy) == "" {
		t.Fatal("an acknowledged bootstrap change must carry a reason a reviewer can judge")
	}

	files, err := bootstrapRuntimeFiles()
	if err != nil {
		t.Fatalf("bootstrap runtime files: %v", err)
	}
	if len(files) != len(baseline.Commands) {
		t.Fatalf("activation verifies %d bootstrap binaries, baseline lists %d",
			len(files), len(baseline.Commands))
	}
	for _, command := range baseline.Commands {
		found := false
		for _, file := range files {
			// slotPath is bin/<command>-<arch>; systemPath ends the same way.
			if strings.Contains(file.slotPath, command+"-") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("baseline lists %q, but activation does not byte-verify it; the baseline "+
				"would promise compatibility the updater never checks", command)
		}
	}
	for _, file := range files {
		if !strings.HasPrefix(file.systemPath, "/usr/libexec/minimalrouter/bootstrap/") {
			t.Fatalf("bootstrap binary %s is not installed under the bootstrap directory the "+
				"baseline documents", file.systemPath)
		}
	}
}
