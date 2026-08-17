package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// createSupportBundle produces a deliberately narrow diagnostic archive.
// It never copies MinimalRouter's SQLite state, PPPoE secret files, WireGuard
// configuration/private keys, TLS private keys, process environments, or shell
// history. Command output is chosen explicitly below rather than recursively
// archiving /etc or /var/lib.
func createSupportBundle(output string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if output == "" {
		output = filepath.Join("/tmp", "minimalrouter-support-"+stamp+".tar.gz")
	}
	work, err := os.MkdirTemp("", "minimalrouter-support-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	writeText := func(name, content string) error {
		path := filepath.Join(work, name)
		return os.WriteFile(path, []byte(content), 0o600)
	}
	command := func(name string, args ...string) string {
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("command: %s %s\nerror: %v\n\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	readSafe := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("unavailable: %v\n", err)
		}
		return string(data)
	}

	metadata := fmt.Sprintf("minimalrouter sanitized support bundle\ncreated_utc=%s\n\nSECURITY NOTE\nThis archive intentionally excludes credentials, private keys, the configuration database, process environments, and shell history.\n", time.Now().UTC().Format(time.RFC3339))
	if err := writeText("README.txt", metadata); err != nil {
		return "", err
	}
	_ = writeText("version.txt", "minimalrouter_version:\n"+readSafe("/etc/minimalrouter/VERSION")+"\ninstalled_marker:\n"+readSafe("/etc/minimalrouter/installed")+"\nalpine_release:\n"+readSafe("/etc/alpine-release"))
	_ = writeText("kernel.txt", command("uname", "-a"))
	_ = writeText("memory.txt", readSafe("/proc/meminfo"))
	_ = writeText("cpu.txt", readSafe("/proc/cpuinfo"))
	_ = writeText("interfaces.txt", command("ip", "-details", "-brief", "link"))
	_ = writeText("addresses.txt", command("ip", "-brief", "address"))
	_ = writeText("routes-ipv4.txt", command("ip", "-4", "route", "show"))
	_ = writeText("routes-ipv6.txt", command("ip", "-6", "route", "show"))
	_ = writeText("neighbors.txt", command("ip", "neighbor", "show"))
	_ = writeText("disks.txt", command("lsblk", "-o", "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,SERIAL"))
	_ = writeText("mounts.txt", command("findmnt", "-r"))
	_ = writeText("openrc.txt", command("rc-status", "-a"))
	_ = writeText("routerd-status.txt", command("rc-service", "routerd", "status"))
	_ = writeText("applyd-status.txt", command("rc-service", "router-applyd", "status"))
	_ = writeText("sshd-status.txt", command("rc-service", "sshd", "status"))
	_ = writeText("listeners.txt", command("ss", "-lntu"))
	_ = writeText("nftables.txt", command("nft", "list", "ruleset"))
	_ = writeText("modules.txt", command("lsmod"))
	_ = writeText("sysctl-routing.txt", strings.Join([]string{
		"net.ipv4.ip_forward=" + strings.TrimSpace(command("sysctl", "-n", "net.ipv4.ip_forward")),
		"net.ipv6.conf.all.forwarding=" + strings.TrimSpace(command("sysctl", "-n", "net.ipv6.conf.all.forwarding")),
		"net.ipv4.conf.all.rp_filter=" + strings.TrimSpace(command("sysctl", "-n", "net.ipv4.conf.all.rp_filter")),
	}, "\n")+"\n")

	if err := tarGzipDirectory(work, output); err != nil {
		return "", err
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return "", err
	}
	return output, nil
}

func tarGzipDirectory(dir, output string) (retErr error) {
	file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); retErr == nil && err != nil {
			retErr = err
		}
	}()

	gz := gzip.NewWriter(file)
	defer func() {
		if err := gz.Close(); retErr == nil && err != nil {
			retErr = err
		}
	}()

	tw := tar.NewWriter(gz)
	defer func() {
		if err := tw.Close(); retErr == nil && err != nil {
			retErr = err
		}
	}()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = entry.Name()
		header.Mode = 0o600
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
