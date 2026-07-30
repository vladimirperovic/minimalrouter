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

	"golang.org/x/sys/unix"
)

var releaseVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

// SlotState records the current, previous, and staged software versions. The
// current and previous symlinks are authoritative at runtime; state.json keeps
// the same information for diagnostics and the pending marker.
type SlotState struct {
	Current  string `json:"current"`
	Previous string `json:"previous"`
	Pending  string `json:"pending"`
}

// SlotManager stages only content covered by an Ed25519-signed manifest. It
// never executes release-provided scripts.
type SlotManager struct {
	Root       string
	TrustedKey ed25519.PublicKey
}

func (m SlotManager) Stage(sourceDir string, manifest *FirmwareManifest) error {
	return m.withLock(func() error {
		if manifest == nil || !releaseVersionPattern.MatchString(manifest.Version) {
			return errors.New("release version is not a safe semantic version")
		}
		if len(m.TrustedKey) != ed25519.PublicKeySize {
			return ErrInvalidPublicKey
		}
		if err := VerifyFirmware(sourceDir, manifest, m.TrustedKey); err != nil {
			return fmt.Errorf("verify staged release: %w", err)
		}

		// Validate existing state before committing a final slot. A corrupt state
		// file must never leave a version directory that blocks a safe retry.
		state, err := m.State()
		if err != nil {
			return err
		}
		finalDir := filepath.Join(m.Root, "slots", manifest.Version)
		if _, err := os.Stat(finalDir); err == nil {
			return errors.New("release version is already staged")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		tempDir, err := os.MkdirTemp(filepath.Join(m.Root, "slots"), ".staging-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tempDir)
		if err := os.Chmod(tempDir, 0o755); err != nil {
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
		if err := syncDir(tempDir); err != nil {
			return fmt.Errorf("sync staged slot: %w", err)
		}
		if err := os.Rename(tempDir, finalDir); err != nil {
			return fmt.Errorf("commit staged slot: %w", err)
		}
		if err := syncDir(filepath.Join(m.Root, "slots")); err != nil {
			_ = os.RemoveAll(finalDir)
			return fmt.Errorf("sync committed slot: %w", err)
		}

		state.Pending = manifest.Version
		if err := m.saveState(state); err != nil {
			// Roll back the directory commit so the same verified version can be
			// retried after the state problem is repaired.
			removeErr := os.RemoveAll(finalDir)
			_ = syncDir(filepath.Join(m.Root, "slots"))
			if removeErr != nil {
				return errors.Join(err, fmt.Errorf("remove uncommitted slot: %w", removeErr))
			}
			return err
		}
		return nil
	})
}

func (m SlotManager) Activate(version string) error {
	return m.withLock(func() error {
		if !releaseVersionPattern.MatchString(version) {
			return errors.New("invalid release version")
		}
		if err := m.requireSlot(version); err != nil {
			return err
		}
		state, err := m.State()
		if err != nil {
			return err
		}
		if state.Current == version {
			state.Pending = ""
			return m.saveState(state)
		}

		old := state
		state.Previous = old.Current
		state.Current = version
		state.Pending = ""
		if err := m.saveState(state); err != nil {
			return err
		}
		if old.Current != "" {
			if err := m.swapLink("previous", old.Current); err != nil {
				_ = m.saveState(old)
				return fmt.Errorf("prepare previous slot pointer: %w", err)
			}
		} else if err := m.removeLink("previous"); err != nil {
			_ = m.saveState(old)
			return err
		}
		if err := m.swapLink("current", version); err != nil {
			restoreErr := m.restoreLinks(old)
			_ = m.saveState(old)
			return errors.Join(fmt.Errorf("activate slot pointer: %w", err), restoreErr)
		}
		return nil
	})
}

func (m SlotManager) Rollback() error {
	return m.withLock(func() error {
		state, err := m.State()
		if err != nil {
			return err
		}
		if state.Previous == "" {
			return errors.New("no previous release slot is available")
		}
		if err := m.requireSlot(state.Previous); err != nil {
			return errors.New("previous release slot is missing")
		}

		old := state
		state.Current, state.Previous = old.Previous, old.Current
		state.Pending = ""
		if err := m.saveState(state); err != nil {
			return err
		}
		if err := m.swapLink("current", state.Current); err != nil {
			_ = m.saveState(old)
			return fmt.Errorf("restore current slot pointer: %w", err)
		}
		if state.Previous != "" {
			if err := m.swapLink("previous", state.Previous); err != nil {
				restoreErr := m.restoreLinks(old)
				_ = m.saveState(old)
				return errors.Join(fmt.Errorf("preserve rollback slot pointer: %w", err), restoreErr)
			}
		} else if err := m.removeLink("previous"); err != nil {
			restoreErr := m.restoreLinks(old)
			_ = m.saveState(old)
			return errors.Join(err, restoreErr)
		}
		return nil
	})
}

func (m SlotManager) State() (SlotState, error) {
	var state SlotState
	data, err := os.ReadFile(filepath.Join(m.Root, "state.json"))
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return SlotState{}, fmt.Errorf("decode update state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return SlotState{}, err
	}

	if current, exists, err := m.linkVersion("current"); err != nil {
		return SlotState{}, err
	} else if exists {
		state.Current = current
	}
	if previous, exists, err := m.linkVersion("previous"); err != nil {
		return SlotState{}, err
	} else if exists {
		state.Previous = previous
	}
	return state, nil
}

func (m SlotManager) ensureRoot() error {
	if strings.TrimSpace(m.Root) == "" {
		return errors.New("update root is required")
	}
	if err := os.MkdirAll(filepath.Join(m.Root, "slots"), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(m.Root, 0o755); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(m.Root, "slots"), 0o755)
}

func (m SlotManager) withLock(fn func() error) error {
	if err := m.ensureRoot(); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(m.Root, ".update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN) //nolint:errcheck
	return fn()
}

func (m SlotManager) saveState(state SlotState) error {
	if err := m.ensureRoot(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
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
	if err := os.Rename(name, filepath.Join(m.Root, "state.json")); err != nil {
		return err
	}
	return syncDir(m.Root)
}

func (m SlotManager) swapLink(name, version string) error {
	if err := m.requireSlot(version); err != nil {
		return err
	}
	tempLink := filepath.Join(m.Root, fmt.Sprintf(".%s-new-%d", name, os.Getpid()))
	_ = os.Remove(tempLink)
	if err := os.Symlink(filepath.Join("slots", version), tempLink); err != nil {
		return err
	}
	if err := os.Rename(tempLink, filepath.Join(m.Root, name)); err != nil {
		_ = os.Remove(tempLink)
		return err
	}
	return syncDir(m.Root)
}

func (m SlotManager) removeLink(name string) error {
	err := os.Remove(filepath.Join(m.Root, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(m.Root)
}

func (m SlotManager) restoreLinks(state SlotState) error {
	var errs []error
	if state.Current == "" {
		errs = append(errs, m.removeLink("current"))
	} else {
		errs = append(errs, m.swapLink("current", state.Current))
	}
	if state.Previous == "" {
		errs = append(errs, m.removeLink("previous"))
	} else {
		errs = append(errs, m.swapLink("previous", state.Previous))
	}
	return errors.Join(errs...)
}

func (m SlotManager) requireSlot(version string) error {
	if !releaseVersionPattern.MatchString(version) {
		return errors.New("invalid release version")
	}
	info, err := os.Stat(filepath.Join(m.Root, "slots", version))
	if err != nil || !info.IsDir() {
		return errors.New("release slot is not staged")
	}
	return nil
}

func (m SlotManager) linkVersion(name string) (string, bool, error) {
	target, err := os.Readlink(filepath.Join(m.Root, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	clean := filepath.Clean(target)
	if filepath.IsAbs(clean) || filepath.Dir(clean) != "slots" {
		return "", false, fmt.Errorf("unsafe %s slot pointer", name)
	}
	version := filepath.Base(clean)
	if !releaseVersionPattern.MatchString(version) {
		return "", false, fmt.Errorf("invalid %s slot pointer", name)
	}
	return version, true, nil
}

func copyRegularFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("release file is missing or unsafe: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
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

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
