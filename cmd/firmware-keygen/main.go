package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func main() {
	privatePath := flag.String("private-key", "", "output path for the raw Ed25519 private key")
	publicPath := flag.String("public-key", "", "output path for the hex-encoded Ed25519 public key")
	flag.Parse()
	if *privatePath == "" || *publicPath == "" {
		fatal(errors.New("--private-key and --public-key are required"))
	}
	publicKey, privateKey, err := firmware.GenerateKeyPair()
	if err != nil {
		fatal(err)
	}
	if err := writeFile(*privatePath, privateKey, 0o600); err != nil {
		fatal(err)
	}
	if err := writeFile(*publicPath, []byte(hex.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		_ = os.Remove(*privatePath)
		fatal(err)
	}
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return os.Chmod(path, mode)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
