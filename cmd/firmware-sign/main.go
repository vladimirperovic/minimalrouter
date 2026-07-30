package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func main() {
	directory := flag.String("dir", "", "directory containing release artifacts")
	keyPath := flag.String("key", "", "file containing a raw or base64-encoded Ed25519 private key")
	version := flag.String("version", "", "release version")
	commit := flag.String("commit", "", "source commit")
	output := flag.String("output", "release-manifest.json", "manifest output path")
	flag.Parse()
	if *directory == "" || *keyPath == "" || *version == "" || *commit == "" {
		fatal(errors.New("--dir, --key, --version, and --commit are required"))
	}
	privateKey, err := loadPrivateKey(*keyPath)
	if err != nil {
		fatal(err)
	}
	manifest, err := firmware.SignFirmware(*directory, privateKey)
	if err != nil {
		fatal(err)
	}
	manifest.Version = *version
	manifest.BuildDate = time.Now().UTC().Format(time.RFC3339)
	manifest.GitCommit = *commit
	if err := firmware.SignManifest(manifest, privateKey); err != nil {
		fatal(err)
	}
	if err := firmware.SaveManifest(manifest, *output); err != nil {
		fatal(err)
	}
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	decoded, decodeErr := base64.StdEncoding.DecodeString(trimmed)
	if decodeErr == nil {
		data = decoded
	} else {
		data = []byte(trimmed)
	}
	if len(data) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key must be %d raw bytes or base64 thereof", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(data), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
