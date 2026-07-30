package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpDoesNotRequireRootOrSigningKey(t *testing.T) {
	t.Setenv("MINIMALROUTER_FIRMWARE_PUBLIC_KEY", t.TempDir()+"/missing.pub")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, 1000, &stdout, &stderr); code != 0 {
		t.Fatalf("help exit code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: router-update") {
		t.Fatalf("help output missing usage: %q", stdout.String())
	}
}

func TestStatusWorksBeforeFirstStagedRelease(t *testing.T) {
	t.Setenv("MINIMALROUTER_UPDATE_ROOT", t.TempDir()+"/updates")
	t.Setenv("MINIMALROUTER_FIRMWARE_PUBLIC_KEY", t.TempDir()+"/missing.pub")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status"}, 1000, &stdout, &stderr); code != 0 {
		t.Fatalf("status exit code = %d, stderr=%q", code, stderr.String())
	}
	for _, field := range []string{`"current"`, `"previous"`, `"pending"`} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("status output missing %s: %q", field, stdout.String())
		}
	}
}

func TestMutationStillRequiresRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"activate", "--version", "1.2.3", "--confirm", "ACTIVATE-UPDATE"}, 1000, &stdout, &stderr); code != 1 {
		t.Fatalf("activate exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "must run as root") {
		t.Fatalf("missing root error: %q", stderr.String())
	}
}
