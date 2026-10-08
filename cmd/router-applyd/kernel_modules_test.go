package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRouterKernelModulesMatchInstalledModuleList(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "alpine", "minimalrouter.modules"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}
	if !reflect.DeepEqual(listed, routerKernelModules) {
		t.Fatalf("router-applyd loads %v, the installed module list has %v", routerKernelModules, listed)
	}
}

func TestPreflightLoadsKernelModulesFirst(t *testing.T) {
	previous := loadKernelModules
	defer func() { loadKernelModules = previous }()
	calls := 0
	loadKernelModules = func() error {
		calls++
		return errors.New("modprobe: module not found")
	}
	dir := t.TempDir()
	candidates := map[string]string{"nftables": filepath.Join(dir, "missing.nft"), "dnsmasq": filepath.Join(dir, "missing.conf")}
	if err := preflight(config.DefaultConfig(), candidates, runtimeVerificationPlan{}); err == nil {
		t.Fatal("preflight of missing candidates unexpectedly succeeded")
	}
	if calls != 1 {
		t.Fatalf("preflight loaded kernel modules %d times, want once before component checks", calls)
	}
}

func TestStartupPreflightLoadsKernelModules(t *testing.T) {
	previous := loadKernelModules
	defer func() { loadKernelModules = previous }()
	calls := 0
	loadKernelModules = func() error { calls++; return nil }
	dir := t.TempDir()
	_ = preflightStartup(config.DefaultConfig(), map[string]string{"nftables": filepath.Join(dir, "missing.nft"), "dnsmasq": filepath.Join(dir, "missing.conf")})
	if calls != 1 {
		t.Fatalf("startup preflight loaded kernel modules %d times, want once", calls)
	}
}
