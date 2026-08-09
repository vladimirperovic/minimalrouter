package main

import (
	"strings"
	"testing"
	"time"
)

func TestDDNSOutputFailureMarkerSanitizesProviderOutput(t *testing.T) {
	output := "inadyn[1]: Error response from DDNS server\n\x1b[31mAuthentication failure\x1b[0m\r\nprovider-detail"
	marker := ddnsOutputFailureMarker(output)
	if marker == "" {
		t.Fatal("expected DDNS failure output to be detected")
	}
	if strings.ContainsAny(marker, "\r\n") {
		t.Fatalf("provider-controlled DDNS error was not flattened: %q", marker)
	}
	if strings.Contains(marker, "\x1b") {
		t.Fatalf("provider-controlled DDNS error retained terminal escape sequences: %q", marker)
	}
}

func TestRunCommandOutputSanitizesNonZeroChildDiagnostics(t *testing.T) {
	_, err := runCommandOutput(time.Second, "/bin/sh", "-c", "printf '\\033[31mprovider-failure\\033[0m\\nsecond-line' >&2; exit 1")
	if err == nil {
		t.Fatal("expected child command failure")
	}
	text := err.Error()
	if strings.ContainsAny(text, "\r\n\x1b") {
		t.Fatalf("child-process diagnostic retained control characters: %q", text)
	}
	if !strings.Contains(text, "provider-failure") || !strings.Contains(text, "second-line") {
		t.Fatalf("sanitized child-process diagnostic lost useful context: %q", text)
	}
}
