//go:build unix

package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestServiceTimeoutCancelsShellChildrenAndOutputPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	configureServiceProcess(cmd)
	start := time.Now()
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("cancelled service succeeded")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatal("service exited before timeout fixture")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("service cancellation exceeded bounded drain: %s", elapsed)
	}
}
