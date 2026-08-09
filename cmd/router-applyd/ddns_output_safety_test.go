package main

import (
	"strings"
	"testing"
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
