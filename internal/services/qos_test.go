package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateQoSUsesOneRootQdisc(t *testing.T) {
	for _, algorithm := range []string{"cake", "fq_codel"} {
		t.Run(algorithm, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.QoS.Enabled = true
			cfg.QoS.Algorithm = algorithm
			script, err := GenerateQoS(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(script, " qdisc add dev eth0 root "); count != 1 {
				t.Fatalf("expected exactly one root qdisc, got %d:\n%s", count, script)
			}
			if strings.Contains(script, " ffff: cake ") {
				t.Fatal("CAKE was incorrectly attached to an ingress handle")
			}
		})
	}
}

func TestGenerateQoSUsesPPPInterfaceWhenWANEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.QoS.Enabled = true
	script, err := GenerateQoS(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "dev ppp0") || strings.Contains(script, "dev eth0") {
		t.Fatalf("PPPoE QoS targeted the wrong interface:\n%s", script)
	}
}

func TestQoSCommandsAreArgvOnlyAndMatchAlgorithm(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QoS.Enabled = true
	cfg.QoS.Algorithm = "cake"
	commands, err := QoSCommands(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 {
		t.Fatalf("expected four direct tc commands (upload, ingress, mirred redirect, ifb0 download), got %d", len(commands))
	}
	for _, args := range commands {
		for _, arg := range args {
			if strings.ContainsAny(arg, ";|&`$()<>") {
				t.Fatalf("shell metacharacter reached QoS argv: %q", arg)
			}
		}
	}
}

func TestQoSDownloadShapingUsesIfbMirrorInsteadOfPoliceDrop(t *testing.T) {
	for _, algorithm := range []string{"cake", "fq_codel"} {
		t.Run(algorithm, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.QoS.Enabled = true
			cfg.QoS.Algorithm = algorithm
			script, err := GenerateQoS(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(script, "police") {
				t.Fatalf("download is still a police/drop policer instead of queuing on %s:\n%s", QoSInterfaceName, script)
			}
			if !strings.Contains(script, "mirred") || !strings.Contains(script, QoSInterfaceName) {
				t.Fatalf("WAN ingress is not redirected into %s:\n%s", QoSInterfaceName, script)
			}
			if strings.Count(script, " qdisc add dev "+QoSInterfaceName+" root ") != 1 {
				t.Fatalf("expected exactly one download root qdisc on %s:\n%s", QoSInterfaceName, script)
			}
		})
	}
}
