package api

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/release"
)

const (
	updateOperationPath   = "/var/lib/minimalrouter/update-operation.json"
	updatePreferencesPath = "/var/lib/minimalrouter/update-preferences.json"
)

// updateService holds the optional release-update subsystem. It is an explicit
// field on Server rather than a package-level registry keyed by *Server: the
// lifetime of a checker and its cache belongs to the server that owns it.
type updateService struct {
	mu          sync.RWMutex
	checker     *release.Checker
	operations  *updateOperationStore
	preferences *updatePreferences
}

// updatePreferences stores update settings that are not part of the network
// configuration. The channel is an update preference, not a router setting: it
// must not enter the canonical config, its schema, or its apply/rollback path.
type updatePreferences struct {
	path string

	mu      sync.Mutex
	channel release.Channel
	loaded  bool
}

func newUpdatePreferences(path string) *updatePreferences {
	return &updatePreferences{path: path}
}

func (p *updatePreferences) loadLocked() {
	if p.loaded {
		return
	}
	p.loaded = true
	// Existing installations have always been offered pre-releases, and the
	// published line is still Beta. Defaulting to stable here would silently
	// stop offering updates an operator already receives, so the default
	// preserves today's behaviour and the choice is exposed in the dashboard.
	p.channel = release.ChannelBeta
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var stored struct {
		Channel string `json:"channel"`
	}
	if json.Unmarshal(data, &stored) != nil {
		return
	}
	if channel, ok := release.ParseChannel(stored.Channel); ok {
		p.channel = channel
	}
}

func (p *updatePreferences) Channel() release.Channel {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	return p.channel
}

func (p *updatePreferences) SetChannel(channel release.Channel) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	data, err := json.MarshalIndent(map[string]string{"channel": string(channel)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(p.path), ".update-preferences-")
	if err != nil {
		return err
	}
	name := temp.Name()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		os.Remove(name)
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		os.Remove(name)
		return err
	}
	if err := temp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p.path); err != nil {
		os.Remove(name)
		return err
	}
	p.channel = channel
	return nil
}

// ConfigureUpdates attaches the release checker and the durable operation
// record, and reconciles any operation interrupted by a restart. It returns the
// checker so the caller owns its lifecycle.
func (s *Server) ConfigureUpdates(catalog release.Catalog, arch string) *release.Checker {
	preferences := newUpdatePreferences(updatePreferencesPath)
	operations := newUpdateOperationStore(updateOperationPath)
	checker := release.NewChecker(catalog, arch, preferences.Channel)

	s.updates.mu.Lock()
	s.updates.checker = checker
	s.updates.operations = operations
	s.updates.preferences = preferences
	s.updates.mu.Unlock()

	s.reconcileInterruptedUpdate(operations, time.Now())
	return checker
}

// reconcileInterruptedUpdate resolves a record left mid-flight by a restart.
// The activation itself restarts routerd, so "the process died during
// activation" is the normal successful path, not a failure: the slot state and
// the version actually running decide the outcome.
func (s *Server) reconcileInterruptedUpdate(operations *updateOperationStore, now time.Time) {
	operation, err := operations.Current()
	if err != nil {
		log.Printf("[UPDATE] Could not read the recorded update operation: %v", err)
		return
	}
	if operation == nil || operation.State.Terminal() {
		return
	}
	state, stateErr := applianceUpdateState()
	if stateErr == nil && operation.State.pastActivation() {
		switch {
		case state.Current == operation.TargetVersion:
			_ = operations.Finish(operation.ID, UpdateSucceeded, "", "", now)
			log.Printf("[UPDATE] Operation %s completed: running %s", operation.ID, state.Current)
			s.appendAudit("firmware.update_succeeded", "local", map[string]string{
				"operation": operation.ID, "version": operation.TargetVersion,
			})
			return
		case state.Current == operation.FromVersion:
			_ = operations.Finish(operation.ID, UpdateRolledBack, "activation_rolled_back",
				"The new release did not become healthy and the previous version was restored.", now)
			log.Printf("[UPDATE] Operation %s rolled back to %s", operation.ID, state.Current)
			s.appendAudit("firmware.update_rolled_back", "local", map[string]string{
				"operation": operation.ID, "version": state.Current,
			})
			return
		}
	}
	recovered, err := operations.RecoverInterrupted(now)
	if err != nil {
		log.Printf("[UPDATE] Could not reconcile the interrupted update: %v", err)
		return
	}
	if recovered != nil {
		log.Printf("[UPDATE] Operation %s marked %s after a restart", recovered.ID, recovered.State)
	}
}

func (s *Server) updateChecker() *release.Checker {
	s.updates.mu.RLock()
	defer s.updates.mu.RUnlock()
	return s.updates.checker
}

func (s *Server) updateSnapshot() release.Snapshot {
	checker := s.updateChecker()
	if checker == nil {
		return release.Snapshot{StaleAfter: release.DefaultStaleAfter}
	}
	return checker.Snapshot()
}

func (s *Server) updateChannel() release.Channel {
	s.updates.mu.RLock()
	preferences := s.updates.preferences
	s.updates.mu.RUnlock()
	if preferences == nil {
		return release.ChannelStable
	}
	return preferences.Channel()
}

func (s *Server) setUpdateChannel(channel release.Channel) error {
	s.updates.mu.RLock()
	preferences := s.updates.preferences
	s.updates.mu.RUnlock()
	if preferences == nil {
		return errors.New("update preferences are not configured")
	}
	return preferences.SetChannel(channel)
}

func (s *Server) updateOperations() *updateOperationStore {
	s.updates.mu.RLock()
	defer s.updates.mu.RUnlock()
	return s.updates.operations
}

func (s *Server) currentUpdateOperation() (*UpdateOperation, error) {
	store := s.updateOperations()
	if store == nil {
		return nil, nil
	}
	return store.Current()
}

func (s *Server) mustOperation(id string) *UpdateOperation {
	operation, err := s.currentUpdateOperation()
	if err != nil || operation == nil || operation.ID != id {
		return nil
	}
	return operation
}

func (s *Server) beginUpdateOperation(operation UpdateOperation) (*UpdateOperation, error) {
	store := s.updateOperations()
	if store == nil {
		return nil, errors.New("update operations are not configured")
	}
	return store.Begin(operation)
}

func (s *Server) advanceOperation(id string, state UpdateState) error {
	store := s.updateOperations()
	if store == nil {
		return errors.New("update operations are not configured")
	}
	return store.Advance(id, state, time.Now())
}

func (s *Server) failOperation(id, code, message string) error {
	store := s.updateOperations()
	if store == nil {
		return errors.New("update operations are not configured")
	}
	log.Printf("[UPDATE] Operation %s failed (%s): %s", id, code, message)
	s.appendAudit("firmware.update_failed", "local", map[string]string{"operation": id, "reason": code})
	if err := store.Finish(id, UpdateFailed, code, message, time.Now()); err != nil {
		return err
	}
	return errors.New(message)
}
