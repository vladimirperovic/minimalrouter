package recovery

import (
	"errors"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func (m Manager) SetWAN(interfaceName string) (config.Snapshot, error) {
	current, err := m.latest()
	if err != nil {
		return config.Snapshot{}, err
	}
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" || interfaceName == current.LAN.Interface {
		return config.Snapshot{}, errors.New("WAN interface must be non-empty and distinct from LAN")
	}
	next := current.DeepCopy()
	next.Revision++
	next.UpdatedAt = time.Now().UTC()
	next.WAN.Interface = interfaceName
	if err := next.Validate(); err != nil {
		return config.Snapshot{}, err
	}
	if err := next.ValidateScenarioSafety(); err != nil {
		return config.Snapshot{}, err
	}
	return m.Store.RecoverySaveConfig(current, next, nil, false)
}

func (m Manager) RestoreLatestSnapshot() (config.Snapshot, string, error) {
	snapshots, err := m.ListSnapshots()
	if err != nil {
		return config.Snapshot{}, "", err
	}
	if len(snapshots) == 0 {
		return config.Snapshot{}, "", errors.New("no recovery snapshots are available")
	}
	latest := snapshots[0]
	for _, candidate := range snapshots[1:] {
		if candidate.CreatedAt > latest.CreatedAt {
			latest = candidate
		}
	}
	undo, err := m.RestoreSnapshot(latest.ID)
	return undo, latest.ID, err
}
