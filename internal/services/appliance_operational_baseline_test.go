package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readApplianceFile(t *testing.T, parts ...string) string {
	t.Helper()
	pathParts := append([]string{"..", ".."}, parts...)
	data, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("read appliance baseline file: %v", err)
	}
	return string(data)
}

func TestRouterKernelBaselineSupportsAsymmetricPathsAndBoundsState(t *testing.T) {
	sysctl := readApplianceFile(t, "packaging", "alpine", "99-minimalrouter.conf")
	for _, required := range []string{
		"net.ipv4.conf.all.rp_filter=2",
		"net.ipv4.conf.default.rp_filter=2",
		"net.netfilter.nf_conntrack_max=131072",
	} {
		if !strings.Contains(sysctl, required) {
			t.Fatalf("router sysctl baseline is missing %q", required)
		}
	}
	if strings.Contains(sysctl, "net.ipv4.conf.all.rp_filter=1") ||
		strings.Contains(sysctl, "net.ipv4.conf.default.rp_filter=1") {
		t.Fatal("strict reverse-path filtering would break valid asymmetric router paths")
	}

	modules := readApplianceFile(t, "packaging", "alpine", "minimalrouter.modules")
	if !strings.Contains(modules, "\nnf_conntrack\n") {
		t.Fatal("nf_conntrack must load before the conntrack sysctl is applied")
	}
}

func TestInstallersProvideClientOnlyClockSynchronization(t *testing.T) {
	for _, name := range []string{"install-dist.sh", "install.sh"} {
		installer := readApplianceFile(t, "packaging", "alpine", name)
		for _, required := range []string{
			"chrony chrony-openrc",
			"pool pool.ntp.org iburst maxsources 4",
			"makestep 1.0 3",
			"port 0",
			"cmdport 0",
			"rc-update add chronyd default",
		} {
			if !strings.Contains(installer, required) {
				t.Fatalf("%s is missing clock-safety setting %q", name, required)
			}
		}
		if strings.Contains(installer, "allow all") || strings.Contains(installer, "cmdallow all") {
			t.Fatalf("%s exposes chronyd as a network service", name)
		}
	}
}

// Live test v0.3.1 (L3): after a signed full install from v0.3.0 to v0.3.1 the
// appliance still named 0.3.0 in /etc/minimalrouter/VERSION, which the support
// bundle and the firstboot banner report. Only signed archives may replace it.
func TestFullInstallerRecordsTheSignedVersion(t *testing.T) {
	installer := readApplianceFile(t, "packaging", "alpine", "install-dist.sh")
	record := "install -m 0644 -o root -g root VERSION /etc/minimalrouter/VERSION"
	at := strings.Index(installer, record)
	if at < 0 {
		t.Fatal("install-dist.sh does not record the installed version in /etc/minimalrouter/VERSION")
	}
	signed := strings.Index(installer, "if [ -f firmware-signing.pub ]; then")
	unsigned := strings.Index(installer, "NOTE: unsigned development archive")
	if signed < 0 || unsigned < 0 || at < signed || at > unsigned {
		t.Fatal("the installed version must be recorded only for signed archives")
	}
}
