package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
)

const (
	dnsmasqLeaseDir  = "/var/lib/minimalrouter-dhcp"
	dnsmasqLeaseFile = dnsmasqLeaseDir + "/dnsmasq.leases"
)

// prepareDnsmasqLeaseState makes the persistent DHCP state usable by the
// unprivileged dnsmasq process. Older installs can already have a root-owned
// empty lease file, so fixing only the parent directory is not sufficient.
func prepareDnsmasqLeaseState() error {
	dnsmasqUser, err := user.Lookup("dnsmasq")
	if err != nil {
		return fmt.Errorf("lookup dnsmasq service account: %w", err)
	}
	uid, err := parseNumericID("dnsmasq uid", dnsmasqUser.Uid)
	if err != nil {
		return err
	}
	gid, err := parseNumericID("dnsmasq gid", dnsmasqUser.Gid)
	if err != nil {
		return err
	}
	return prepareDnsmasqLeaseStateAt(dnsmasqLeaseDir, dnsmasqLeaseFile, uid, gid)
}

func parseNumericID(label, value string) (int, error) {
	var id int
	if _, err := fmt.Sscanf(value, "%d", &id); err != nil || id < 0 {
		return 0, fmt.Errorf("%s is invalid: %q", label, value)
	}
	return id, nil
}

func prepareDnsmasqLeaseStateAt(dir, leaseFile string, uid, gid int) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create persistent DHCP lease directory: %w", err)
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return fmt.Errorf("own persistent DHCP lease directory: %w", err)
	}
	if err := os.Chmod(dir, 0750); err != nil {
		return fmt.Errorf("secure persistent DHCP lease directory: %w", err)
	}

	// Do not follow an unexpected symlink at this privileged path. The second
	// lookup handles the harmless race where dnsmasq creates the file between
	// the initial lookup and our O_EXCL create.
	for attempt := 0; attempt < 2; attempt++ {
		info, err := os.Lstat(leaseFile)
		if errors.Is(err, os.ErrNotExist) {
			file, createErr := os.OpenFile(leaseFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
			if createErr == nil {
				if closeErr := file.Close(); closeErr != nil {
					return fmt.Errorf("close persistent DHCP lease file: %w", closeErr)
				}
				break
			}
			if errors.Is(createErr, os.ErrExist) {
				continue
			}
			return fmt.Errorf("create persistent DHCP lease file: %w", createErr)
		}
		if err != nil {
			return fmt.Errorf("inspect persistent DHCP lease file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("persistent DHCP lease file is a symlink")
		}
		if !info.Mode().IsRegular() {
			return errors.New("persistent DHCP lease file is not regular")
		}
		break
	}

	if err := os.Chown(leaseFile, uid, gid); err != nil {
		return fmt.Errorf("own persistent DHCP lease file: %w", err)
	}
	// dnsmasq needs read/write access; routerd is a supplementary dnsmasq
	// group member and therefore retains read-only lease telemetry access.
	if err := os.Chmod(leaseFile, 0640); err != nil {
		return fmt.Errorf("secure persistent DHCP lease file: %w", err)
	}
	return nil
}
