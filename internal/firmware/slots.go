package firmware

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var releaseVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

// SlotState is the crash-safe update pointer state.
type SlotState struct {
	Current  string `json:"current,omitempty"`
	Previous string `json:"previous,omitempty"`
	Pending  string `json:"pending,omitempty"`
}

// SlotManager stages only content covered by an Ed25519-signed manifest. It
// never executes release-provided scripts.
type SlotManager struct {
	Root       string
	TrustedKey ed25519.PublicKey
}

func (m SlotManager) Stage(sourceDir string, manifest *FirmwareManifest) error {
	if !releaseVersionPattern.MatchString(manifest.Version) {
		return errors.New("release version is not a safe semantic version")
	}
	if err := VerifyFirmware(sourceDir, manifest, m.TrustedKey); err != nil {
		return fmt.Errorf("verify staged release: %w", err)
	}
	if err := m.ensureRoot(); err != nil {
		return err
	}
	finalDir := filepath.Join(m.Root, "slots", manifest.Version)
	if _, err := os.Stat(finalDir); err == nil {
		return errors.New("release version is already staged")
	}
	tempDir, err := os.MkdirTemp(filepath.Join(m.Root, "slots"), ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return err
	}
	for relative := range manifest.Files {
		clean := filepath.Clean(relative)
		source := filepath.Join(sourceDir, clean)
		destination := filepath.Join(tempDir, clean)
		if err := copyRegularFile(source, destination); err != nil {
			return err
		}
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return fmt.Errorf("commit staged slot: %w", err)
	}
	state, err := m.State()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	state.Pending = manifest.Version
	return m.saveState(state)
}

func (m SlotManager) Activate(version string) error {
	if !releaseVersionPattern.MatchString(version) {
		return errors.New("invalid release version")
	}
	if info, err := os.Stat(filepath.Join(m.Root, "slots", version)); err != nil || !info.IsDir() {
		return errors.New("release slot is not staged")
	}
	state, err := m.State()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state.Current != version {
		state.Previous = state.Current
		state.Current = version
	}
	state.Pending = ""
	if err := m.swapCurrentLink(version); err != nil {
		return err
	}
	return m.saveState(state)
}

func (m SlotManager) Rollback() error {
	state, err := m.State()
	if err != nil {
		return err
	}
	if state.Previous == "" {
		return errors.New("no previous release slot is available")
	}
	if info, err := os.Stat(filepath.Join(m.Root, "slots", state.Previous)); err != nil || !info.IsDir() {
		return errors.New("previous release slot is missing")
	}
	state.Current, state.Previous = state.Previous, state.Current
	state.Pending = ""
	if err := m.swapCurrentLink(state.Current); err != nil {
		return err
	}
	return m.saveState(state)
}

func (m SlotManager) State() (SlotState, error) {
	data, err := os.ReadFile(filepath.Join(m.Root, "state.json"))
	if err != nil {
		return SlotState{}, err
	}
	var state SlotState
	if err := json.Unmarshal(data, &state); err != nil {
		return SlotState{}, fmt.Errorf("decode update state: %w", err)
	}
	return state, nil
}

func (m SlotManager) ensureRoot() error {
	if len(m.TrustedKey) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	if strings.TrimSpace(m.Root) == "" {
		return errors.New("update root is required")
	}
	if err := os.MkdirAll(filepath.Join(m.Root, "slots"), 0o700); err != nil {
		return err
	}
	return os.Chmod(m.Root, 0o700)
}

func (m SlotManager) saveState(state SlotState) error {
	if err := m.ensureRoot(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(m.Root, ".state-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(m.Root, "state.json"))
}

func (m SlotManager) swapCurrentLink(version string) error {
	if err := m.ensureRoot(); err != nil {
		return err
	}
	tempLink := filepath.Join(m.Root, ".current-new")
	_ = os.Remove(tempLink)
	if err := os.Symlink(filepath.Join("slots", version), tempLink); err != nil {
		return err
	}
	return os.Rename(tempLink, filepath.Join(m.Root, "current"))
}

func copyRegularFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("release file is missing or unsafe: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
