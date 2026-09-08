package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

// The full installer is an explicit root trust decision, separate from signed
// staging. This read-only preflight must run before package or runtime writes.
func inspectInstallation(manager firmware.SlotManager, source, systemRoot string) (*firmware.FirmwareManifest, string, error) {
	inventory, err := firmware.InspectLocalPayload(source)
	if err != nil {
		return nil, "", err
	}
	if err := firmware.ValidateApplianceArchitecture(inventory, runtime.GOARCH); err != nil {
		return nil, "", err
	}
	state, err := manager.State()
	if err != nil {
		return nil, "", err
	}
	floor, floorErr := state.UpgradeFloor()
	pubPath := filepath.Join(source, "firmware-signing.pub")
	_, keyErr := os.Stat(pubPath)
	if errors.Is(keyErr, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(source, "release-manifest.json")); !errors.Is(err, os.ErrNotExist) {
			return nil, "", errors.New("manifest without a distribution trust key")
		}
		// A root-installed development tree may replace runtime files, but it
		// cannot invent, lower or erase an authenticated version floor.
		if floorErr != nil {
			floor = state.MinimumVersion
		}
		return inventory, floor, nil
	}
	if keyErr != nil {
		return nil, "", keyErr
	}
	key, err := firmware.LoadTrustedPublicKey(pubPath)
	if err != nil {
		return nil, "", err
	}
	installedKey := rootedPath(systemRoot, defaultPublicKey)
	if _, err := os.Stat(installedKey); err == nil {
		pinned, err := firmware.LoadTrustedPublicKey(installedKey)
		if err != nil {
			return nil, "", err
		}
		if !bytes.Equal(key, pinned) {
			return nil, "", errors.New("refusing to replace the installed firmware trust anchor")
		}
		key = pinned
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	manifest, err := firmware.LoadManifest(filepath.Join(source, "release-manifest.json"))
	if err != nil {
		return nil, "", fmt.Errorf("signed full installation requires release-manifest.json: %w", err)
	}
	if err := firmware.ValidateReleaseCandidate(source, manifest, key); err != nil {
		return nil, "", err
	}
	// A full installer executes/copies the whole tree, so reject unsigned extra
	// files too. The detached manifest itself cannot hash its own signature.
	for path := range inventory.Files {
		if path != "release-manifest.json" && manifest.Files[path] != inventory.Files[path] {
			return nil, "", fmt.Errorf("full installation contains unsigned file: %s", path)
		}
	}
	if floorErr != nil {
		// Migration from the Golden installer predating signed baseline storage:
		// use its root-installed VERSION only as an additional conservative lower
		// bound; the new floor itself comes from the verified candidate manifest.
		data, err := os.ReadFile(rootedPath(systemRoot, "/etc/minimalrouter/VERSION"))
		if err != nil {
			return nil, "", floorErr
		}
		floor = strings.TrimSpace(string(data))
		if _, err := firmware.CompareReleaseVersions(floor, floor); err != nil {
			return nil, "", floorErr
		}
	}
	if floor != "" {
		cmp, err := firmware.CompareReleaseVersions(manifest.Version, floor)
		if err != nil {
			return nil, "", err
		}
		// Same-version full reinstall repairs an existing installation; normal
		// stage/activate continue to require a strictly forward version.
		if cmp < 0 {
			return nil, "", errors.New("refusing full-install downgrade below the installed version floor")
		}
	}
	return inventory, manifest.Version, nil
}

// The installer has synchronized all OS/package writes before this command.
// Recheck and fsync each exact integration file and its ancestor directories
// before the slot manager may publish a baseline and remove the install fence.
func syncInstalledIntegration(source, systemRoot string) error {
	if systemRoot == "" {
		systemRoot = "/"
	}
	systemRoot, err := filepath.Abs(systemRoot)
	if err != nil {
		return err
	}
	bootstrap, err := bootstrapRuntimeFiles()
	if err != nil {
		return err
	}
	files := append(append([]runtimeLayoutFile(nil), runtimeLayoutFiles...), bootstrap...)
	if err := verifyLayoutFiles(source, systemRoot, files); err != nil {
		return err
	}
	for _, item := range files {
		path := rootedPath(systemRoot, item.systemPath)
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		if err := errors.Join(syncErr, file.Close()); err != nil {
			return err
		}
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			directory, err := os.Open(parent)
			if err != nil {
				return err
			}
			syncErr := directory.Sync()
			if err := errors.Join(syncErr, directory.Close()); err != nil {
				return err
			}
			if parent == systemRoot || parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return nil
}
