package firmware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const installationJournal = "installation.json"

var errUnknownInstalledVersion = errors.New("installed bootstrap version is unknown; use a verified full distribution installation to establish the version floor")

func (m SlotManager) checkInstallation() error {
	if _, err := os.Stat(filepath.Join(m.Root, installationJournal)); err == nil {
		return errors.New("full installation is incomplete; finish the full installer before update or rollback")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// BeginInstallation fences ordinary stage/activate/rollback before the full
// installer makes its first change. Interrupted installs remain fail-closed.
func (m SlotManager) BeginInstallation(minimum string) error {
	return m.withLock(func() error {
		if err := m.recoverOperation(); err != nil {
			return err
		}
		state, err := m.stateWithoutOperation()
		if err != nil {
			return err
		}
		minimum, err = installationMinimum(state, minimum)
		if err != nil {
			return err
		}
		return m.writeAtomicJSON(installationJournal, ".install-intent-", 0o600, SlotState{MinimumVersion: minimum})
	})
}

// UpgradeFloor separates the monotonic trust policy from the current slot.
// Legacy synthetic baselines contain no authenticated version information and
// must not silently stand in for an installed release version.
func (s SlotState) UpgradeFloor() (string, error) {
	floor := s.MinimumVersion
	if floor != "" {
		if _, err := parseReleaseVersion(floor); err != nil {
			return "", err
		}
	}
	if strings.HasPrefix(strings.TrimPrefix(s.Current, "v"), "0.0.0+bootstrap.") {
		if floor == "" {
			return "", errUnknownInstalledVersion
		}
		return floor, nil
	}
	if s.Current != "" {
		if _, err := parseReleaseVersion(s.Current); err != nil {
			return "", err
		}
		if floor == "" {
			return s.Current, nil
		}
		cmp, err := compareReleaseVersions(s.Current, floor)
		if err != nil {
			return "", err
		}
		if cmp > 0 {
			floor = s.Current
		}
	}
	return floor, nil
}

func normalizeCopiedFile(root, relative string) error {
	if err := os.Chmod(filepath.Join(root, relative), ApplianceFileMode(relative)); err != nil {
		return err
	}
	for dir := filepath.Dir(relative); dir != "."; dir = filepath.Dir(dir) {
		if err := os.Chmod(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// InspectLocalPayload inventories an explicitly trusted root-operated full
// installation. It does not turn an unsigned development payload into firmware.
func InspectLocalPayload(root string) (*FirmwareManifest, error) {
	m := &FirmwareManifest{Files: make(map[string]string)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsafe installer payload: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		m.Files[relative] = hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ValidateApplianceFileModes(root, m); err != nil {
		return nil, err
	}
	return m, nil
}

// InstallBaseline is only used by the root full installer, never by the web
// update bridge. minimum must come from its preflight (verified signed release
// or the preserved installed floor). Every full install starts a new rollback
// generation: an application-only rollback cannot undo replaced OS integration.
func (m SlotManager) InstallBaseline(source string, inventory *FirmwareManifest, minimum string) error {
	return m.withLock(func() error {
		if err := m.recoverOperation(); err != nil {
			return err
		}
		installed, err := m.stateWithoutOperation()
		if err != nil {
			return err
		}
		minimum, err = installationMinimum(installed, minimum)
		if err != nil {
			return err
		}
		data, err := signedPayload(inventory)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		version := "0.0.0+bootstrap." + hex.EncodeToString(sum[:16])
		temp, err := os.MkdirTemp(filepath.Join(m.Root, "slots"), ".install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)
		if err := os.Chmod(temp, 0o755); err != nil {
			return err
		}
		for relative := range inventory.Files {
			if !filepath.IsLocal(relative) || filepath.Clean(relative) != relative {
				return fmt.Errorf("unsafe installer path: %s", relative)
			}
			if err := copyRegularFile(filepath.Join(source, relative), filepath.Join(temp, relative)); err != nil {
				return err
			}
			if err := normalizeCopiedFile(temp, relative); err != nil {
				return err
			}
		}
		copied, err := InspectLocalPayload(temp)
		if err != nil {
			return err
		}
		for path, hash := range inventory.Files {
			if copied.Files[path] != hash {
				return fmt.Errorf("installer payload changed during copy: %s", path)
			}
		}
		if err := syncPayloadTree(temp); err != nil {
			return fmt.Errorf("persist complete baseline: %w", err)
		}
		final := filepath.Join(m.Root, "slots", version)
		if _, err := os.Stat(final); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(temp, final); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			existing, err := InspectLocalPayload(final)
			if err != nil {
				return err
			}
			if len(existing.Files) != len(inventory.Files) {
				return errors.New("existing baseline contains a different file inventory")
			}
			for path, hash := range inventory.Files {
				if existing.Files[path] != hash {
					return errors.New("existing baseline contents differ")
				}
			}
			if err := syncPayloadTree(final); err != nil {
				return err
			}
		}
		if err := syncDir(filepath.Join(m.Root, "slots")); err != nil {
			return err
		}
		old, err := m.stateWithoutOperation()
		if err != nil {
			return err
		}
		next := SlotState{Current: version, MinimumVersion: minimum}
		if err := m.commitOperation("install", old, next, func() error {
			if err := m.removeLink("previous"); err != nil {
				return err
			}
			return m.swapLink("current", version)
		}); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(m.Root, installationJournal)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDir(m.Root)
	})
}

// Persist file bytes/modes and every directory entry, bottom-up, before the
// parent slot pointer or completion state can outlive any copied subtree.
func syncPayloadTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsafe baseline entry: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		return errors.Join(syncErr, file.Close())
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncDir(directories[i]); err != nil {
			return err
		}
	}
	return nil
}

func (m SlotManager) applyInstallationFloor(state *SlotState) error {
	state.InstallationPending = false
	data, err := os.ReadFile(filepath.Join(m.Root, installationJournal))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var intent SlotState
	if err := json.Unmarshal(data, &intent); err != nil {
		return err
	}
	state.InstallationPending = true
	if intent.MinimumVersion != "" {
		if _, err := parseReleaseVersion(intent.MinimumVersion); err != nil {
			return err
		}
		if state.MinimumVersion == "" {
			state.MinimumVersion = intent.MinimumVersion
		} else {
			cmp, err := compareReleaseVersions(intent.MinimumVersion, state.MinimumVersion)
			if err != nil {
				return err
			}
			if cmp > 0 {
				state.MinimumVersion = intent.MinimumVersion
			}
		}
	}
	return nil
}

func installationMinimum(state SlotState, proposed string) (string, error) {
	floor, err := state.UpgradeFloor()
	if err != nil && !errors.Is(err, errUnknownInstalledVersion) {
		return "", err
	}
	if proposed == "" {
		if floor != "" {
			return "", errors.New("full installation cannot erase a recorded version floor; repeat preflight")
		}
		return "", nil
	}
	if _, err := parseReleaseVersion(proposed); err != nil {
		return "", err
	}
	if floor != "" {
		cmp, err := compareReleaseVersions(proposed, floor)
		if err != nil {
			return "", err
		}
		if cmp < 0 {
			return "", errors.New("full installation cannot lower the recorded version floor")
		}
	}
	return proposed, nil
}
