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
		if removeErr := os.Remove(*privatePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove incomplete private key: %w", removeErr))
		}
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
		return cleanupFailedFile(file, path, err)
	}
	if err := file.Sync(); err != nil {
		return cleanupFailedFile(file, path, err)
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(path)
		return errors.Join(err, removeErr)
	}
	return os.Chmod(path, mode)
}

func cleanupFailedFile(file *os.File, path string, primary error) error {
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(primary, closeErr, removeErr)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
