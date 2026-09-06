package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func newPauseTestStore(t *testing.T) (*devicePauseStore, *[]apply.DevicePause) {
	t.Helper()
	live := []apply.DevicePause{}
	cfg := config.DefaultConfig()
	cfg.LAN.CIDR = "192.168.0.1/16"
	s := &devicePauseStore{
		path:         filepath.Join(t.TempDir(), "pauses.json"),
		save:         func(path string, data []byte) error { return atomicWrite(path, data, 0600) },
		clearJournal: clearDevicePauseJournal,
		now:          func() time.Time { return time.Unix(2000000000, 0) },
		validate:     func(ip string) (string, error) { return validatePauseIPForConfig(ip, cfg) },
		firewall:     func(pauses []apply.DevicePause) error { live = append([]apply.DevicePause{}, pauses...); return nil },
	}
	return s, &live
}

func requirePauseCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *apply.ActionError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func seedPause(t *testing.T, s *devicePauseStore) []apply.DevicePause {
	t.Helper()
	pauses, err := s.change("192.168.1.10", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return pauses
}

func TestDevicePauseDurablePauseResume(t *testing.T) {
	s, live := newPauseTestStore(t)
	pauses, err := s.change("192.168.1.10", 900, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pauses) != 1 || pauses[0].UntilUnix != s.now().Unix()+900 || !reflect.DeepEqual(pauses, *live) {
		t.Fatalf("unexpected pause state: %v / %v", pauses, *live)
	}
	state, err := s.load()
	if err != nil || state.Phase != "applied" || !reflect.DeepEqual(state.Pauses, pauses) {
		t.Fatalf("durable state: %+v, %v", state, err)
	}
	if _, err := os.Stat(s.path + ".pending"); !os.IsNotExist(err) {
		t.Fatalf("undo journal remains: %v", err)
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions: %v, %v", info, err)
	}
	pauses, err = s.change("192.168.1.10", 0, true)
	if err != nil || len(pauses) != 0 || len(*live) != 0 {
		t.Fatalf("resume: %v / %v, %v", pauses, *live, err)
	}
}

func TestDevicePauseNFTFailureRollsBackPauseAndResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprint(resume), func(t *testing.T) {
			s, live := newPauseTestStore(t)
			previous := seedPause(t, s)
			firewall := s.firewall
			calls := 0
			s.firewall = func(pauses []apply.DevicePause) error {
				calls++
				if calls == 2 {
					_ = firewall(pauses)
					return errors.New("nft ACK lost after mutation")
				}
				return firewall(pauses)
			}
			ip := "192.168.1.11"
			if resume {
				ip = "192.168.1.10"
			}
			result, err := s.change(ip, 0, resume)
			requirePauseCode(t, err, apply.ActionFailed)
			if result != nil || !reflect.DeepEqual(*live, previous) {
				t.Fatalf("runtime rollback: %v / %v", result, *live)
			}
			state, err := s.load()
			if err != nil || state.Phase != "applied" || !reflect.DeepEqual(state.Pauses, previous) {
				t.Fatalf("durable rollback: %+v, %v", state, err)
			}
		})
	}
}

func TestDevicePauseIntentFailureNeverChangesNFT(t *testing.T) {
	s, live := newPauseTestStore(t)
	previous := seedPause(t, s)
	save := s.save
	s.save = func(path string, data []byte) error { return errors.New("disk full") }
	_, err := s.change("192.168.1.10", 0, true)
	requirePauseCode(t, err, apply.ActionRecoveryRequired)
	if !reflect.DeepEqual(*live, previous) {
		t.Fatalf("intent failure changed firewall: %v", *live)
	}
	s.save = save
	if got, err := s.reconcile(); err != nil || !reflect.DeepEqual(got, previous) {
		t.Fatalf("recovery: %v, %v", got, err)
	}
}

func TestDevicePauseCommitRenameFailureAndRollbackFailureSurviveRestart(t *testing.T) {
	s, live := newPauseTestStore(t)
	previous := seedPause(t, s)
	save, firewall := s.save, s.firewall
	s.save = func(path string, data []byte) error {
		if err := save(path, data); err != nil {
			return err
		}
		if path == s.path {
			return errors.New("directory fsync failed after rename")
		}
		return nil
	}
	calls := 0
	s.firewall = func(pauses []apply.DevicePause) error {
		calls++
		if calls == 3 {
			return errors.New("rollback nft failed")
		}
		return firewall(pauses)
	}
	_, err := s.change("192.168.1.10", 0, true)
	requirePauseCode(t, err, apply.ActionRecoveryRequired)
	if len(*live) != 0 {
		t.Fatal("test did not reach the ambiguous resume state")
	}
	// Discard process memory; the durable undo journal must override the candidate
	// state file even though the candidate rename actually succeeded.
	restarted := *s
	restarted.uncertain, restarted.save, restarted.firewall = nil, save, firewall
	got, err := restarted.reconcile()
	if err != nil || !reflect.DeepEqual(got, previous) || !reflect.DeepEqual(*live, previous) {
		t.Fatalf("restart recovery: %v / %v, %v", got, *live, err)
	}
	if _, err := os.Stat(s.path + ".pending"); !os.IsNotExist(err) {
		t.Fatalf("recovered journal remains: %v", err)
	}
}

func TestDevicePauseJournalRemovalFailureRollsBack(t *testing.T) {
	s, live := newPauseTestStore(t)
	previous := seedPause(t, s)
	clear := s.clearJournal
	calls := 0
	s.clearJournal = func(path string) error {
		calls++
		if err := clear(path); err != nil {
			return err
		}
		if calls == 1 {
			return errors.New("directory sync after unlink failed")
		}
		return nil
	}
	_, err := s.change("192.168.1.10", 0, true)
	requirePauseCode(t, err, apply.ActionFailed)
	if !reflect.DeepEqual(*live, previous) {
		t.Fatalf("cleanup failure did not roll back: %v", *live)
	}
}

func TestDevicePauseStatusNeverReportsUnrestoredStateOrPrunesFile(t *testing.T) {
	s, _ := newPauseTestStore(t)
	expired := []apply.DevicePause{{IP: "192.168.1.10", UntilUnix: s.now().Unix() - 1}}
	data, _ := json.Marshal(expired)
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.reconcile()
	if err != nil || len(got) != 0 {
		t.Fatalf("expiry: %v, %v", got, err)
	}
	after, _ := os.ReadFile(s.path)
	if string(after) != string(data) {
		t.Fatal("status silently rewrote legacy/expired durable state")
	}
	s.firewall = func([]apply.DevicePause) error { return errors.New("nft unavailable") }
	got, err = s.reconcile()
	requirePauseCode(t, err, apply.ActionRecoveryRequired)
	if got != nil {
		t.Fatal("failed restore exposed successful status")
	}
}

func TestDevicePauseCorruptStateFailsClosed(t *testing.T) {
	for _, data := range []string{"{}", "null", "[", `{"version":1,"phase":"candidate","pauses":[]}`, `[{"ip":"192.168.1.10","until_unix":-1}]`, `[{"ip":"192.168.1.10","until_unix":0},{"ip":"192.168.1.10","until_unix":0}]`, strings.Repeat(" ", apply.MaxServiceActionResponseBytes+1)} {
		t.Run(fmt.Sprintf("%.30q", data), func(t *testing.T) {
			s, _ := newPauseTestStore(t)
			if err := os.WriteFile(s.path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			s.firewall = func([]apply.DevicePause) error { t.Fatal("corrupt state reached nft"); return nil }
			_, err := s.reconcile()
			requirePauseCode(t, err, apply.ActionRecoveryRequired)
		})
	}
}

func TestDevicePauseLimitAllowsUpdateAndResume(t *testing.T) {
	s, _ := newPauseTestStore(t)
	pauses := make([]apply.DevicePause, maxDevicePauses)
	for i := range pauses {
		pauses[i].IP = fmt.Sprintf("192.168.%d.%d", i/254+2, i%254+1)
	}
	if err := s.persist(devicePauseRecord{Version: 1, Phase: "applied", Pauses: pauses}); err != nil {
		t.Fatal(err)
	}
	_, err := s.change("192.168.10.20", 0, false)
	requirePauseCode(t, err, apply.ActionConflict)
	if got, err := s.change(pauses[0].IP, 900, false); err != nil || len(got) != maxDevicePauses {
		t.Fatalf("update at limit: %d, %v", len(got), err)
	}
	if got, err := s.change(pauses[0].IP, 0, true); err != nil || len(got) != maxDevicePauses-1 {
		t.Fatalf("resume at limit: %d, %v", len(got), err)
	}
}

func TestServiceActionRootGuard(t *testing.T) {
	complete := &transactionRecord{CompletedAt: time.Now(), Response: apply.ApplyResponse{Success: true}}
	recovery := &transactionRecord{CompletedAt: time.Now(), Response: apply.ApplyResponse{RecoveryRequired: true}}
	for _, test := range []struct {
		name       string
		pending    *pendingConfirmation
		pendingErr error
		record     *transactionRecord
		recordErr  error
		memory     *transactionRecord
		blocked    bool
	}{
		{name: "clean", record: complete},
		{name: "first run", pendingErr: os.ErrNotExist, recordErr: os.ErrNotExist},
		{name: "pending", pending: &pendingConfirmation{}, blocked: true},
		{name: "pending unreadable", pendingErr: errors.New("bad JSON"), blocked: true},
		{name: "incomplete", record: &transactionRecord{}, blocked: true},
		{name: "recovery", record: recovery, blocked: true},
		{name: "unreadable record", recordErr: errors.New("bad JSON"), blocked: true},
		{name: "memory recovery overrides disk", record: complete, memory: recovery, blocked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateServiceActionMutationState(test.pending, test.pendingErr, test.record, test.recordErr, test.memory)
			if (err != nil) != test.blocked {
				t.Fatalf("guard: %v", err)
			}
		})
	}
}

func TestServiceActionRootAllowlistAndLANValidation(t *testing.T) {
	for _, req := range []serviceActionRequest{{Action: "shell"}, {Action: apply.DeviceActionPause, Seconds: 1}, {Action: apply.DeviceActionResume, Seconds: 900}, {Action: apply.DeviceActionStatus, IP: "192.168.1.10"}, {Action: apply.ServiceActionWANReconnect, IP: "192.168.1.10"}} {
		requirePauseCode(t, validateServiceActionRequest(req), apply.ActionInvalid)
	}
	cfg := config.DefaultConfig()
	for _, ip := range []string{"; flush ruleset", "::1", cfg.LAN.IPAddress, "203.0.113.10"} {
		if _, err := validatePauseIPForConfig(ip, cfg); err == nil {
			t.Fatalf("accepted %q", ip)
		}
	}
}

func TestDevicePauseNFTBatchIsAtomicAndValidated(t *testing.T) {
	cfg := config.DefaultConfig()
	path := filepath.Join(t.TempDir(), "pause.nft")
	for _, checkFails := range []bool{false, true} {
		var calls []string
		run := func(binary string, args ...string) error {
			if binary != "/usr/sbin/nft" {
				t.Fatalf("unexpected command %s", binary)
			}
			calls = append(calls, strings.Join(args, " "))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			batch := string(data)
			if !strings.HasPrefix(batch, "delete table inet minimalrouter_pause\ntable inet minimalrouter_pause") || strings.Contains(batch, "flush ruleset") {
				t.Fatalf("unsafe batch: %s", batch)
			}
			if checkFails {
				return errors.New("nft rejected candidate")
			}
			return nil
		}
		err := applyDevicePauseFirewallWith(cfg, []apply.DevicePause{{IP: "192.168.1.10"}}, path, true, run, time.Now())
		if checkFails && (err == nil || len(calls) != 1) {
			t.Fatalf("failed check activated candidate: %v, %v", calls, err)
		}
		if !checkFails && (err != nil || len(calls) != 2 || !strings.HasPrefix(calls[0], "-c -f ") || !strings.HasPrefix(calls[1], "-f ")) {
			t.Fatalf("batch calls: %v, %v", calls, err)
		}
	}
	cfg.WAN.Interface = "eth0\"; flush ruleset"
	if _, err := renderDevicePauseFirewall(cfg, nil, time.Now()); err == nil {
		t.Fatal("accepted injected interface")
	}
}

func TestDevicePauseTableInventoryErrorsAreNotAbsence(t *testing.T) {
	for _, test := range []struct {
		body           string
		err            error
		exists, failed bool
	}{
		{body: `{"nftables":[]}`},
		{body: `{"nftables":[{"table":{"family":"inet","name":"minimalrouter_pause"}}]}`, exists: true},
		{body: `{"nftables":[{"table":{"family":"ip","name":"minimalrouter_pause"}}]}`},
		{body: `{}`, failed: true}, {body: `{`, failed: true}, {err: errors.New("permission denied"), failed: true},
	} {
		exists, err := devicePauseTableExists(func(string, ...string) (string, error) { return test.body, test.err })
		if exists != test.exists || (err != nil) != test.failed {
			t.Fatalf("inventory %q: %v, %v", test.body, exists, err)
		}
	}
}

type actionTestListener struct {
	closed  chan struct{}
	accepts atomic.Int32
}

func (l *actionTestListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	l.accepts.Add(1)
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}
func (l *actionTestListener) Close() error   { close(l.closed); return nil }
func (l *actionTestListener) Addr() net.Addr { return &net.UnixAddr{Name: "test", Net: "unix"} }

func TestServiceActionConnectionsAreBounded(t *testing.T) {
	listener := &actionTestListener{closed: make(chan struct{})}
	release := make(chan struct{})
	entered := make(chan struct{}, maxServiceActionConnections)
	done := make(chan struct{})
	go func() {
		serveServiceActionConnections(listener, func(conn net.Conn) { defer conn.Close(); entered <- struct{}{}; <-release })
		close(done)
	}()
	for i := 0; i < maxServiceActionConnections; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("listener did not fill slots")
		}
	}
	if got := listener.accepts.Load(); got != maxServiceActionConnections {
		t.Fatalf("accepted %d connections", got)
	}
	_ = listener.Close()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
}

func TestDevicePauseRenderValidatesTrustedLANAndRetainsWANOnlyDrops(t *testing.T) {
	cfg := config.DefaultConfig()
	now := time.Unix(2000000000, 0)
	for _, ip := range []string{"203.0.113.10", cfg.LAN.IPAddress, "::1", "192.168.1.10; flush ruleset"} {
		if _, err := renderDevicePauseFirewall(cfg, []apply.DevicePause{{IP: ip}}, now); err == nil {
			t.Fatalf("renderer accepted %q", ip)
		}
	}
	batch, err := renderDevicePauseFirewall(cfg, []apply.DevicePause{{IP: "192.168.1.10", UntilUnix: now.Unix() - 1}, {IP: "192.168.1.11", UntilUnix: now.Unix() + 30}, {IP: "192.168.1.12"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(batch, "192.168.1.10") || !strings.Contains(batch, "192.168.1.11 timeout 30s") || !strings.Contains(batch, "192.168.1.12") {
		t.Fatalf("expiry rendering: %s", batch)
	}
	for _, required := range []string{`ip saddr @blocked_ipv4 oifname "ppp*" drop`, `type filter hook forward priority -10; policy accept;`} {
		if !strings.Contains(batch, required) {
			t.Fatalf("missing %q", required)
		}
	}
	if strings.Contains(batch, "flush ruleset") || strings.Contains(batch, "hook input") {
		t.Fatal("pause modified canonical firewall policy")
	}
}
