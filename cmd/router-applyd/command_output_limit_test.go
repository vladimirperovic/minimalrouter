package main

import (
	"bytes"
	"testing"
)

func TestBoundedCommandOutputPreservesSmallOutput(t *testing.T) {
	buffer := newBoundedCommandOutput(16)
	input := []byte("small-output")
	n, err := buffer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) {
		t.Fatalf("write count = %d, want %d", n, len(input))
	}
	got, truncated := buffer.snapshot()
	if truncated {
		t.Fatal("small output was marked truncated")
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("captured output = %q, want %q", got, input)
	}
}

func TestBoundedCommandOutputCapsMemoryAndReportsFullWrite(t *testing.T) {
	buffer := newBoundedCommandOutput(8)
	input := []byte("abcdefghijkl")
	n, err := buffer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) {
		t.Fatalf("write count = %d, want %d", n, len(input))
	}
	got, truncated := buffer.snapshot()
	if !truncated {
		t.Fatal("oversized output was not marked truncated")
	}
	if string(got) != "abcdefgh" {
		t.Fatalf("captured output = %q, want cap prefix", got)
	}
}

func TestBoundedCommandOutputRemainsCappedAcrossWrites(t *testing.T) {
	buffer := newBoundedCommandOutput(8)
	_, _ = buffer.Write([]byte("12345"))
	_, _ = buffer.Write([]byte("67890"))
	_, _ = buffer.Write([]byte("more"))
	got, truncated := buffer.snapshot()
	if !truncated {
		t.Fatal("multi-write overflow was not marked truncated")
	}
	if string(got) != "12345678" {
		t.Fatalf("captured output = %q, want exactly 8 bytes", got)
	}
}
