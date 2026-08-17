package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mrnetwork "github.com/vladimirperovic/minimalrouter/internal/network"
)

func testRecommendation() mrnetwork.RoleRecommendation {
	return mrnetwork.RoleRecommendation{
		WAN: "eth0",
		LAN: "eth1",
		Interfaces: []mrnetwork.InterfaceInfo{
			{Name: "eth0", Physical: true, Carrier: true, Score: 135},
			{Name: "eth1", Physical: true, Carrier: true, Score: 135},
		},
	}
}

func TestRecommendRolesUniquePPPoEResponderWins(t *testing.T) {
	wan, lan, reason := recommendRoles(testRecommendation(), map[string]bool{"eth0": false, "eth1": true})
	if wan != "eth1" || lan != "eth0" {
		t.Fatalf("got WAN=%q LAN=%q, want WAN=eth1 LAN=eth0", wan, lan)
	}
	if reason != "only this interface answered PPPoE discovery" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestRecommendRolesAmbiguousPPPoEFallsBackToHeuristic(t *testing.T) {
	wan, lan, reason := recommendRoles(testRecommendation(), map[string]bool{"eth0": true, "eth1": true})
	if wan != "eth0" || lan != "eth1" {
		t.Fatalf("got WAN=%q LAN=%q, want heuristic WAN=eth0 LAN=eth1", wan, lan)
	}
	if reason != "PPPoE answered on multiple interfaces; manual confirmation is required" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestRecommendRolesNoPPPoEUsesHeuristic(t *testing.T) {
	wan, lan, reason := recommendRoles(testRecommendation(), map[string]bool{})
	if wan != "eth0" || lan != "eth1" {
		t.Fatalf("got WAN=%q LAN=%q, want WAN=eth0 LAN=eth1", wan, lan)
	}
	if reason != "no PPPoE discovery response; falling back to link/default-route hardware scoring" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestWriteProvisionIsRootOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.json")
	cfg := provision{WANInterface: "eth0", LANInterface: "eth1", PPPoEUsername: "u", PPPoEPassword: "secret", AdminPassword: "abcdefghijkl", LANIPAddress: defaultLANIP}
	if err := writeProvision(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("provision mode = %o, want 600", got)
	}
}

func TestConfirmEnterAcceptsDefaultYes(t *testing.T) {
	ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
	got, err := ui.confirm("Continue?", true)
	if err != nil || !got {
		t.Fatalf("got %v, %v; want true, nil", got, err)
	}
}

func TestConfirmEnterAcceptsDefaultNo(t *testing.T) {
	ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
	got, err := ui.confirm("Continue?", false)
	if err != nil || got {
		t.Fatalf("got %v, %v; want false, nil", got, err)
	}
}

func TestConfirmExplicitNoOverridesDefaultYes(t *testing.T) {
	ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("n\n"))}
	got, err := ui.confirm("Continue?", true)
	if err != nil || got {
		t.Fatalf("got %v, %v; want false, nil", got, err)
	}
}

func TestConfirmInvalidAnswerReprompts(t *testing.T) {
	ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("maybe\n\n"))}
	got, err := ui.confirm("Continue?", true)
	if err != nil || !got {
		t.Fatalf("got %v, %v; want true, nil", got, err)
	}
}

func TestPPPoEBasedSuggestionRequiresExplicitConfirmation(t *testing.T) {
	for _, reason := range []string{
		"only this interface answered PPPoE discovery",
		"PPPoE answered on multiple interfaces; manual confirmation is required",
	} {
		pppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
		if !pppoeBased {
			t.Fatalf("reason %q must be treated as requiring explicit confirmation", reason)
		}
		ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
		got, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Fatalf("Enter must not accept PPPoE-based suggestion for %q", reason)
		}
	}
}

func TestNonPPPoESuggestionStillAllowsSafeEnterDefault(t *testing.T) {
	reason := "no PPPoE discovery response; falling back to link/default-route hardware scoring"
	pppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
	ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
	got, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("Enter should accept a non-PPPoE suggestion when heuristic roles are otherwise valid")
	}
}
