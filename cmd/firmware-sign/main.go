package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	publicKeyOutput := flag.String("public-key-output", "", "optional path for the hex-encoded pinned public key; write this inside --dir so it is signed")
	flag.Parse()
	if *directory == "" || *keyPath == "" || *version == "" || *commit == "" {
		fatal(errors.New("--dir, --key, --version, and --commit are required"))
	}
	privateKey, err := loadPrivateKey(*keyPath)
	if err != nil {
		fatal(err)
	}
	if *publicKeyOutput != "" {
		if err := writePublicKey(*publicKeyOutput, privateKey.Public().(ed25519.PublicKey)); err != nil {
			fatal(err)
		}
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
	// A raw Ed25519 key is arbitrary binary data. Never trim it: a valid key may
	// legitimately begin or end with bytes classified as whitespace.
	if len(data) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(append([]byte(nil), data...)), nil
	}
	trimmed := strings.TrimSpace(string(data))
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key must be %d raw bytes or base64 thereof", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

func writePublicKey(path string, key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return firmware.ErrInvalidPublicKey
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create public key directory: %w", err)
	}
	data := []byte(hex.EncodeToString(key) + "\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write pinned public key: %w", err)
	}
	return os.Chmod(path, 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
