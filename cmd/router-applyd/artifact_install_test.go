package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func testArtifacts(t *testing.T) map[string]artifact {
	t.Helper()
	dir := t.TempDir()
	generated := make(map[string]artifact)
	for _, name := range restoreArtifacts {
		generated[name] = artifact{path: filepath.Join(dir, name), data: []byte("candidate " + name), mode: 0640}
	}
	return generated
}

func TestUnrelatedSaveRepairsServiceAccessAfterReplacingArtifacts(t *testing.T) {
	generated := testArtifacts(t)
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.SquidProxy.Enabled = true
	ddnsRepairs, squidRepairs := 0, 0
	installer := artifactInstaller{
		write: atomicWrite,
		secureDDNS: func() error {
			data, err := os.ReadFile(generated["cf-ddns"].path)
			if err != nil || string(data) != string(generated["cf-ddns"].data) {
				t.Fatal("DDNS repair did not see the newly installed candidate")
			}
			ddnsRepairs++
			return nil
		},
		secureSquid: func() error { squidRepairs++; return nil },
	}
	if err := installer.install(cfg, generated); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(generated["cf-ddns"].path)
	if err != nil {
		t.Fatal(err)
	}
	// The DDNS configuration remains identical; only an unrelated field changes.
	cfg.QoS.DownloadLimitMbps++
	if err := installer.install(cfg, generated); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(generated["cf-ddns"].path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(first, second) {
		t.Fatal("fixture did not exercise atomic inode replacement")
	}
	if ddnsRepairs != 2 || squidRepairs != 2 {
		t.Fatalf("service access was not repaired on each replacement: DDNS=%d Squid=%d", ddnsRepairs, squidRepairs)
	}
}

func TestArtifactPermissionFailureRejectsInstallation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.SquidProxy.Enabled = true
	want := errors.New("service group unavailable")
	installer := artifactInstaller{
		write:       atomicWrite,
		secureDDNS:  func() error { return want },
		secureSquid: func() error { t.Fatal("installation continued after DDNS permission failure"); return nil },
	}
	if err := installer.install(cfg, testArtifacts(t)); !errors.Is(err, want) {
		t.Fatalf("error=%v, want permission failure", err)
	}
}

func TestDisabledOptionalServicesDoNotRequireAccounts(t *testing.T) {
	installer := artifactInstaller{
		write:       atomicWrite,
		secureDDNS:  func() error { t.Fatal("disabled DDNS requires an account"); return nil },
		secureSquid: func() error { t.Fatal("disabled Squid requires an account"); return nil },
	}
	if err := installer.install(config.DefaultConfig(), testArtifacts(t)); err != nil {
		t.Fatal(err)
	}
}
