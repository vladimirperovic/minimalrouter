package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

type runtimeLayoutFile struct {
	slotPath   string
	systemPath string
	mode       os.FileMode
}

var runtimeLayoutFiles = []runtimeLayoutFile{
	{slotPath: "compatibility.json", systemPath: "/etc/minimalrouter/compatibility.json", mode: 0o644},
	{slotPath: "init.d/routerd", systemPath: "/etc/init.d/routerd", mode: 0o755},
	{slotPath: "init.d/router-applyd", systemPath: "/etc/init.d/router-applyd", mode: 0o755},
	{slotPath: "init.d/pppoe-wan", systemPath: "/etc/init.d/pppoe-wan", mode: 0o755},
	{slotPath: "sysctl/99-minimalrouter.conf", systemPath: "/etc/sysctl.d/99-minimalrouter.conf", mode: 0o644},
	{slotPath: "modules/minimalrouter.conf", systemPath: "/etc/modules-load.d/minimalrouter.conf", mode: 0o644},
	{slotPath: "logrotate/minimalrouter", systemPath: "/etc/logrotate.d/minimalrouter", mode: 0o644},
	{slotPath: "ip-up.d-minimalrouter-qos", systemPath: "/etc/ppp/ip-up.d/minimalrouter-qos", mode: 0o755},
}

func rootedPath(root, absolute string) string {
	if root == "" || root == "/" {
		return absolute
	}
	return filepath.Join(root, strings.TrimPrefix(absolute, "/"))
}

// verifyRuntimeLayoutCompatibility deliberately refuses to mutate operating-
// system integration files during an A/B activation. Those files run as root
// outside the slot and cannot safely be rolled back by only changing a symlink.
// If a release changes them, install the full signed distribution first; an
// ordinary A/B slot is allowed only when the installed integration layer is an
// exact content/mode match for the candidate release. compatibility.json is
// part of that boundary: config schema, RPC protocol, or bootstrap ABI changes
// therefore force a full installer instead of being discovered on next boot.
func verifyRuntimeLayoutCompatibility(updateRoot, version, systemRoot string) error {
	slotRoot := filepath.Join(updateRoot, "slots", version)
	for _, item := range runtimeLayoutFiles {
		candidate, err := os.ReadFile(filepath.Join(slotRoot, item.slotPath))
		if err != nil {
			return fmt.Errorf("candidate runtime layout missing %s: %w", item.slotPath, err)
		}
		installedPath := rootedPath(systemRoot, item.systemPath)
		installed, err := os.ReadFile(installedPath)
		if err != nil {
			return fmt.Errorf("installed runtime layout missing %s; run the full distribution installer before A/B activation", item.systemPath)
		}
		if !bytes.Equal(candidate, installed) {
			return fmt.Errorf("installed runtime layout differs at %s; run the full distribution installer before A/B activation", item.systemPath)
		}
		info, err := os.Stat(installedPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != item.mode {
			return fmt.Errorf("installed runtime layout has unsafe mode at %s; run the full distribution installer", item.systemPath)
		}
	}

	leaseDir := rootedPath(systemRoot, "/var/lib/minimalrouter-dhcp")
	if info, err := os.Stat(leaseDir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
		return errors.New("persistent DHCP runtime state is not compatible; run the full distribution installer before A/B activation")
	}
	return nil
}

const serviceCommandTimeout = 25 * time.Second

var serviceCommand = func(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), serviceCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/sbin/rc-service", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("rc-service %s timed out after %s", strings.Join(args, " "), serviceCommandTimeout)
		}
		text := strings.TrimSpace(string(output))
		if len(text) > 300 {
			text = text[:300]
		}
		if text != "" {
			return fmt.Errorf("rc-service %s: %w: %s", strings.Join(args, " "), err, text)
		}
		return fmt.Errorf("rc-service %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// restartRuntimePair eliminates mixed-slot RPC windows: stop the management
// plane first, restart the privileged helper from the new current slot, then
// start routerd. If either daemon later crashes, OpenRC will restart it from
// the same current slot.
func restartRuntimePair() error {
	if err := serviceCommand("routerd", "stop"); err != nil {
		return err
	}
	if err := serviceCommand("router-applyd", "restart"); err != nil {
		return err
	}
	if err := serviceCommand("routerd", "start"); err != nil {
		return err
	}
	if err := serviceCommand("router-applyd", "status"); err != nil {
		return err
	}
	return serviceCommand("routerd", "status")
}

func activateAndRestart(manager firmware.SlotManager, version, systemRoot string) error {
	state, err := manager.State()
	if err != nil {
		return fmt.Errorf("read update state: %w", err)
	}
	if state.Current == "" {
		return errors.New("no rollback baseline slot is registered; rerun the full distribution installer before the first A/B activation")
	}
	if err := verifyRuntimeLayoutCompatibility(manager.Root, version, systemRoot); err != nil {
		return err
	}
	if err := manager.Activate(version); err != nil {
		return err
	}
	if err := restartRuntimePair(); err == nil {
		return nil
	} else {
		activationErr := err
		rollbackErr := manager.Rollback()
		if rollbackErr == nil {
			rollbackErr = restartRuntimePair()
		}
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("new slot failed to start: %w", activationErr),
				fmt.Errorf("automatic rollback failed: %w", rollbackErr),
			)
		}
		return fmt.Errorf("new slot failed to start and was automatically rolled back: %w", activationErr)
	}
}

func rollbackAndRestart(manager firmware.SlotManager) error {
	if err := manager.Rollback(); err != nil {
		return err
	}
	if err := restartRuntimePair(); err != nil {
		return fmt.Errorf("rollback pointer changed but services did not restart cleanly: %w", err)
	}
	return nil
}
