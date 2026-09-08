package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const (
	devicePauseStatePath        = "/var/lib/minimalrouter-applyd/device-pauses.json"
	devicePauseNftPath          = "/run/minimalrouter/device-pauses.nft"
	maxDevicePauses             = apply.MaxDevicePauses
	maxServiceActionConnections = 8
)

var actionProcessStartedAt = time.Now()

type serviceActionRequest struct {
	Action  string `json:"action"`
	IP      string `json:"ip,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

type serviceActionResponse struct {
	Success bool                `json:"success"`
	Error   string              `json:"error,omitempty"`
	Code    string              `json:"code,omitempty"`
	Pauses  []apply.DevicePause `json:"pauses,omitempty"`
}

// main hardens the process before removing/recreating the primary apply socket.
// The mtime guard below prevents a stale socket left by a crashed previous
// process from making this listener reachable before that sequence completes.
func init() {
	go startServiceActionListenerAfterApplySocket()
}

func startServiceActionListenerAfterApplySocket() {
	<-runtimeAdmissionReady
	for {
		info, err := os.Stat(apply.DefaultSocketPath)
		if err == nil && !info.ModTime().Before(actionProcessStartedAt) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(apply.ServiceActionSocketPath)
	listener, err := net.Listen("unix", apply.ServiceActionSocketPath)
	if err != nil {
		log.Printf("service action socket unavailable: %v", err)
		return
	}
	defer listener.Close()
	if err := secureSocketForRouterd(apply.ServiceActionSocketPath); err != nil {
		log.Printf("cannot secure service action socket: %v", err)
		_ = os.Remove(apply.ServiceActionSocketPath)
		return
	}
	applyMu.Lock()
	if _, err := devicePauses.reconcile(); err != nil {
		log.Printf("device pause restore unavailable: %v", err)
	}
	applyMu.Unlock()
	log.Printf("router-applyd fixed actions listening on unix://%s", apply.ServiceActionSocketPath)
	serveServiceActionConnections(listener, handleServiceActionConnection)
}

// Acquire before Accept: idle readers and peers waiting on applyMu together
// cannot create an unbounded number of descriptors or goroutines.
func serveServiceActionConnections(listener net.Listener, handle func(net.Conn)) {
	available := make(chan struct{}, maxServiceActionConnections)
	for {
		available <- struct{}{}
		conn, err := listener.Accept()
		if err != nil {
			<-available
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("service action accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go func() { defer func() { <-available }(); handle(conn) }()
	}
}

func handleServiceActionConnection(conn net.Conn) {
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(requestReadTimeout)); err != nil {
		return
	}
	if err := validatePeer(conn); err != nil {
		writeServiceActionResponse(conn, serviceActionResponse{Error: "unauthorized local peer"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(conn, 4097))
	if err != nil || len(data) > 4096 {
		writeServiceActionResponse(conn, serviceActionResponse{Code: apply.ActionInvalid, Error: "invalid or oversized action request"})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request serviceActionRequest
	if err := decoder.Decode(&request); err != nil {
		writeServiceActionResponse(conn, serviceActionResponse{Code: apply.ActionInvalid, Error: "invalid action request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeServiceActionResponse(conn, serviceActionResponse{Code: apply.ActionInvalid, Error: "action request must contain exactly one object"})
		return
	}
	if err := validateServiceActionRequest(request); err != nil {
		writeServiceActionResponse(conn, serviceActionFailure(err))
		return
	}

	applyMu.Lock()
	defer applyMu.Unlock()
	if err := guardServiceActionMutation(); err != nil {
		writeServiceActionResponse(conn, serviceActionFailure(err))
		return
	}

	switch request.Action {
	case apply.DeviceActionStatus:
		pauses, err := devicePauses.reconcile()
		if err != nil {
			writeServiceActionResponse(conn, serviceActionFailure(err))
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	case apply.DeviceActionPause:
		pauses, err := pauseDevice(request.IP, request.Seconds)
		if err != nil {
			log.Printf("device pause failed: %v", err)
			writeServiceActionResponse(conn, serviceActionFailure(err))
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	case apply.DeviceActionResume:
		pauses, err := resumeDevice(request.IP)
		if err != nil {
			log.Printf("device resume failed: %v", err)
			writeServiceActionResponse(conn, serviceActionFailure(err))
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	}

	if err := runFixedServiceAction(request.Action); err != nil {
		log.Printf("service action %q failed: %v", request.Action, err)
		writeServiceActionResponse(conn, serviceActionFailure(err))
		return
	}
	log.Printf("service action %q completed", request.Action)
	writeServiceActionResponse(conn, serviceActionResponse{Success: true})
}

func writeServiceActionResponse(conn net.Conn, response serviceActionResponse) {
	if err := conn.SetWriteDeadline(time.Now().Add(responseWriteTimeout)); err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func runFixedServiceAction(action string) error {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return fmt.Errorf("trusted last-good configuration unavailable")
	}
	switch action {
	case apply.ServiceActionWANReconnect:
		return recoverWAN(*cfg, runFixed, verifyWAN)
	case apply.ServiceActionDNSDHCPRestart:
		if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			return fmt.Errorf("restart dnsmasq: %w", err)
		}
		if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
			return fmt.Errorf("dnsmasq unhealthy after restart: %w", err)
		}
		return nil
	case apply.ServiceActionWireGuardRestart:
		return restartWireGuardServices(*cfg, activateWireGuard, activateWireGuardClient,
			runFixedOutput, runFixed, verifyWireGuardDNSListener)
	default:
		return fmt.Errorf("service action is not allowlisted")
	}
}

// Every mutation is guarded in the root helper as well as in routerd.
func guardServiceActionMutation() error {
	pending, pendingErr := loadPendingConfirmation()
	record, recordErr := loadLastTransaction()
	return validateServiceActionMutationState(pending, pendingErr, record, recordErr, lastTransactionMemory)
}

func validateServiceActionMutationState(pending *pendingConfirmation, pendingErr error, record *transactionRecord, recordErr error, memory *transactionRecord) error {
	if pending != nil || (pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist)) {
		return pauseError(apply.ActionConflict, "configuration confirmation or recovery is active")
	}
	if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
		return pauseError(apply.ActionRecoveryRequired, "configuration transaction state is unreadable")
	}
	for _, tx := range []*transactionRecord{record, memory} {
		if tx != nil && (tx.CompletedAt.IsZero() || tx.Response.RecoveryRequired) {
			return pauseError(apply.ActionRecoveryRequired, "configuration transaction requires recovery")
		}
	}
	return nil
}

func validateServiceActionRequest(request serviceActionRequest) error {
	switch request.Action {
	case apply.DeviceActionPause:
		if request.Seconds != 0 && request.Seconds != 900 && request.Seconds != 3600 {
			return pauseError(apply.ActionInvalid, "unsupported pause duration")
		}
	case apply.DeviceActionResume:
		if request.Seconds != 0 {
			return pauseError(apply.ActionInvalid, "resume does not accept a pause duration")
		}
	case apply.DeviceActionStatus, apply.ServiceActionWANReconnect, apply.ServiceActionDNSDHCPRestart, apply.ServiceActionWireGuardRestart:
		if request.IP != "" || request.Seconds != 0 {
			return pauseError(apply.ActionInvalid, "unexpected action parameters")
		}
	default:
		return pauseError(apply.ActionInvalid, "service action is not allowlisted")
	}
	return nil
}

func pauseError(code, message string) error { return &apply.ActionError{Code: code, Message: message} }

func serviceActionFailure(err error) serviceActionResponse {
	var actionErr *apply.ActionError
	if errors.As(err, &actionErr) {
		return serviceActionResponse{Code: actionErr.Code, Error: actionErr.Message}
	}
	log.Printf("fixed action failed: %v", err)
	return serviceActionResponse{Code: apply.ActionFailed, Error: "privileged action failed"}
}

func validatePauseIP(value string) (string, error) {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return "", pauseError(apply.ActionUnavailable, "trusted last-good configuration unavailable")
	}
	return validatePauseIPForConfig(value, *cfg)
}

func validatePauseIPForConfig(value string, cfg config.SystemConfig) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil {
		return "", pauseError(apply.ActionInvalid, "device address must be IPv4")
	}
	_, lan, err := net.ParseCIDR(cfg.LAN.CIDR)
	if err != nil || lan.IP.To4() == nil {
		return "", pauseError(apply.ActionUnavailable, "trusted LAN range is unavailable")
	}
	if !lan.Contains(ip.To4()) {
		return "", pauseError(apply.ActionInvalid, "device address is outside the trusted LAN")
	}
	if ip.Equal(net.ParseIP(cfg.LAN.IPAddress)) {
		return "", pauseError(apply.ActionInvalid, "router LAN address cannot be paused")
	}
	return ip.To4().String(), nil
}

// Pauses always holds the last committed set. Pending is an undo intent: after
// a crash or ambiguous nft result, restore Pauses before serving status/actions.
// No desired candidate is ever exposed as applied state.
type devicePauseRecord struct {
	Version int                 `json:"version"`
	Phase   string              `json:"phase"`
	Pauses  []apply.DevicePause `json:"pauses"`
}

type devicePauseStore struct {
	path         string
	save         func(string, []byte) error
	clearJournal func(string) error
	firewall     func([]apply.DevicePause) error
	validate     func(string) (string, error)
	now          func() time.Time
	// memory retains undo intent if rename succeeded but directory sync failed.
	// It is accessed only under applyMu, just like the on-disk record.
	uncertain *devicePauseRecord
}

var devicePauses = &devicePauseStore{
	path:         devicePauseStatePath,
	save:         func(path string, data []byte) error { return atomicWrite(path, data, 0600) },
	clearJournal: clearDevicePauseJournal,
	firewall:     applyDevicePauseFirewall,
	validate:     validatePauseIP,
	now:          time.Now,
}

func (s *devicePauseStore) load() (devicePauseRecord, error) {
	if s.uncertain != nil {
		return *s.uncertain, nil
	}
	record := devicePauseRecord{Version: 1, Phase: "applied", Pauses: []apply.DevicePause{}}
	file, err := os.Open(s.path + ".pending")
	journal := err == nil
	if errors.Is(err, os.ErrNotExist) {
		file, err = os.Open(s.path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, apply.MaxServiceActionResponseBytes+1))
	if err != nil {
		return record, err
	}
	if len(data) > apply.MaxServiceActionResponseBytes {
		return record, errors.New("device pause state exceeds size limit")
	}
	data = bytes.TrimSpace(data)
	// Read the old array format without rewriting or silently discarding state.
	if len(data) > 0 && data[0] == '[' {
		err = json.Unmarshal(data, &record.Pauses)
	} else {
		record = devicePauseRecord{}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&record)
		if err == nil {
			if trailing := decoder.Decode(&struct{}{}); trailing != io.EOF {
				err = errors.New("trailing pause state")
			}
		}
	}
	if err != nil {
		return record, err
	}
	if record.Version != 1 || (record.Phase != "applied" && record.Phase != "pending" && record.Phase != "recovery_required") {
		return record, errors.New("invalid device pause transaction state")
	}
	if len(record.Pauses) > maxDevicePauses {
		return record, errors.New("device pause state exceeds count limit")
	}
	if journal && record.Phase == "applied" {
		return record, errors.New("invalid device pause journal")
	}
	seen := make(map[string]bool, len(record.Pauses))
	for _, pause := range record.Pauses {
		ip := net.ParseIP(pause.IP)
		if ip == nil || ip.To4() == nil || ip.To4().String() != pause.IP || pause.UntilUnix < 0 || seen[pause.IP] {
			return record, errors.New("invalid device pause record")
		}
		seen[pause.IP] = true
	}
	return record, nil
}

func (s *devicePauseStore) persist(record devicePauseRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	path := s.path
	if record.Phase != "applied" {
		path += ".pending"
	}
	return s.save(path, data)
}

// The undo journal is removed only after both the nft ACK and durable state
// commit. A crash before that point restores the previous confirmed set.
func clearDevicePauseJournal(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *devicePauseStore) finish(record devicePauseRecord) error {
	record.Phase = "applied"
	if err := s.persist(record); err != nil {
		return err
	}
	return s.clearJournal(s.path + ".pending")
}

// Only expiry is filtered here. The firewall callback must validate every
// active entry from one trusted config snapshot (renderDevicePauseFirewall),
// avoiding one last-good read and full config validation per device.
func (s *devicePauseStore) active(pauses []apply.DevicePause) ([]apply.DevicePause, error) {
	active := make([]apply.DevicePause, 0, len(pauses))
	for _, pause := range pauses {
		if pause.UntilUnix == 0 || pause.UntilUnix > s.now().Unix() {
			active = append(active, pause)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].IP < active[j].IP })
	return active, nil
}

// reconcile deliberately reasserts the committed nft set before status. A read
// never prunes durable state, and failed restore never returns a success list.
func (s *devicePauseStore) reconcile() ([]apply.DevicePause, error) {
	record, err := s.load()
	if err != nil {
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause state is unreadable; recovery required")
	}
	pauses, err := s.active(record.Pauses)
	if err != nil {
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause state cannot be validated against trusted LAN")
	}
	if err := s.firewall(pauses); err != nil {
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause firewall restore failed; recovery required")
	}
	if record.Phase != "applied" || s.uncertain != nil {
		record.Phase = "applied"
		if err := s.finish(record); err != nil {
			return nil, pauseError(apply.ActionRecoveryRequired, "device pause recovery outcome could not be persisted")
		}
		s.uncertain = nil
	}
	return pauses, nil
}

func (s *devicePauseStore) transition(previous, next []apply.DevicePause) ([]apply.DevicePause, error) {
	undo := devicePauseRecord{Version: 1, Phase: "pending", Pauses: append([]apply.DevicePause{}, previous...)}
	// Keep undo in memory before writing; a failed directory fsync may follow
	// a successful rename. No nft mutation is allowed without durable intent.
	s.uncertain = &undo
	if err := s.persist(undo); err != nil {
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause intent could not be persisted; refresh status for recovery")
	}
	if err := s.firewall(next); err == nil {
		result := devicePauseRecord{Version: 1, Phase: "applied", Pauses: next}
		if err := s.finish(result); err == nil {
			s.uncertain = nil
			return next, nil
		}
	}
	// Re-establish durable undo before rollback: a result write may have renamed
	// the candidate despite reporting failure. Keep recovery explicit throughout.
	undo.Phase = "recovery_required"
	intentErr := s.persist(undo)
	rollbackErr := s.firewall(previous)
	if intentErr != nil || rollbackErr != nil {
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause rollback could not be completed; recovery required")
	}
	undo.Phase = "applied"
	if err := s.finish(undo); err != nil {
		undo.Phase = "recovery_required"
		return nil, pauseError(apply.ActionRecoveryRequired, "device pause rollback outcome could not be persisted")
	}
	s.uncertain = nil
	return nil, pauseError(apply.ActionFailed, "device pause action failed; previous pause state restored")
}

func (s *devicePauseStore) change(value string, seconds int, resume bool) ([]apply.DevicePause, error) {
	if seconds != 0 && seconds != 900 && seconds != 3600 {
		return nil, pauseError(apply.ActionInvalid, "unsupported pause duration")
	}
	ip, err := s.validate(value)
	if err != nil {
		return nil, err
	}
	previous, err := s.reconcile()
	if err != nil {
		return nil, err
	}
	next := make([]apply.DevicePause, 0, len(previous)+1)
	for _, pause := range previous {
		if pause.IP != ip {
			next = append(next, pause)
		}
	}
	if !resume {
		if len(next) >= maxDevicePauses {
			return nil, pauseError(apply.ActionConflict, "device pause limit reached")
		}
		until := int64(0)
		if seconds > 0 {
			until = s.now().Add(time.Duration(seconds) * time.Second).Unix()
		}
		next = append(next, apply.DevicePause{IP: ip, UntilUnix: until})
	}
	sort.Slice(next, func(i, j int) bool { return next[i].IP < next[j].IP })
	return s.transition(previous, next)
}

func pauseDevice(value string, seconds int) ([]apply.DevicePause, error) {
	return devicePauses.change(value, seconds, false)
}
func resumeDevice(value string) ([]apply.DevicePause, error) {
	return devicePauses.change(value, 0, true)
}

func applyDevicePauseFirewall(pauses []apply.DevicePause) error {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return pauseError(apply.ActionUnavailable, "trusted last-good configuration unavailable")
	}
	exists, err := devicePauseTableExists(runFixedOutput)
	if err != nil {
		return err
	}
	return applyDevicePauseFirewallWith(*cfg, pauses, devicePauseNftPath, exists, runFixed, time.Now())
}

// A failed table probe is not evidence of absence. In that case nft's additive
// table syntax could merge old elements and falsely report a completed resume.
func devicePauseTableExists(output func(string, ...string) (string, error)) (bool, error) {
	text, err := output("/usr/sbin/nft", "-j", "list", "tables")
	if err != nil {
		return false, fmt.Errorf("inspect pause firewall: %w", err)
	}
	var list struct {
		NFTables []struct {
			Table *struct {
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"table"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		return false, err
	}
	if list.NFTables == nil {
		return false, errors.New("invalid nft table inventory")
	}
	for _, item := range list.NFTables {
		if item.Table != nil && item.Table.Family == "inet" && item.Table.Name == "minimalrouter_pause" {
			return true, nil
		}
	}
	return false, nil
}

func applyDevicePauseFirewallWith(cfg config.SystemConfig, pauses []apply.DevicePause, path string, exists bool, run func(string, ...string) error, now time.Time) error {
	batch, err := renderDevicePauseFirewall(cfg, pauses, now)
	if err != nil {
		return err
	}
	if exists {
		batch = "delete table inet minimalrouter_pause\n" + batch
	}
	// Atomic replacement also prevents following an existing runtime symlink.
	if err := atomicWrite(path, []byte(batch), 0600); err != nil {
		return err
	}
	defer os.Remove(path)
	if err := run("/usr/sbin/nft", "-c", "-f", path); err != nil {
		return fmt.Errorf("validate pause firewall: %w", err)
	}
	// Delete and creation are one nft netlink transaction; an error cannot
	// expose a gap between the old and new rules. Success is the kernel ACK.
	if err := run("/usr/sbin/nft", "-f", path); err != nil {
		return fmt.Errorf("apply pause firewall: %w", err)
	}
	return nil
}

func renderDevicePauseFirewall(cfg config.SystemConfig, pauses []apply.DevicePause, now time.Time) (string, error) {
	if len(pauses) > maxDevicePauses {
		return "", errors.New("device pause limit exceeded")
	}
	if cfg.WAN.Interface != "" && !interfaceNamePattern.MatchString(cfg.WAN.Interface) {
		return "", errors.New("invalid WAN interface")
	}
	var elements []string
	seen := make(map[string]bool, len(pauses))
	for _, pause := range pauses {
		ip, err := validatePauseIPForConfig(pause.IP, cfg)
		if err != nil {
			return "", err
		}
		if pause.UntilUnix < 0 || seen[ip] {
			return "", errors.New("invalid pause expiry or duplicate address")
		}
		seen[ip] = true
		if pause.UntilUnix > 0 {
			remaining := pause.UntilUnix - now.Unix()
			if remaining <= 0 {
				continue
			}
			ip += fmt.Sprintf(" timeout %ds", remaining)
		}
		elements = append(elements, ip)
	}
	var b strings.Builder
	b.WriteString("table inet minimalrouter_pause {\n  set blocked_ipv4 {\n    type ipv4_addr\n    flags timeout\n")
	if len(elements) > 0 {
		b.WriteString("    elements = { " + strings.Join(elements, ", ") + " }\n")
	}
	b.WriteString("  }\n  chain forward {\n    type filter hook forward priority -10; policy accept;\n")
	// Pause only Internet forwarding; the canonical table still owns all other
	// default-deny and trusted-network protections. Never flush the ruleset.
	if cfg.WAN.Interface != "" {
		b.WriteString(fmt.Sprintf("    ip saddr @blocked_ipv4 oifname \"%s\" drop\n", cfg.WAN.Interface))
	}
	b.WriteString("    ip saddr @blocked_ipv4 oifname \"ppp*\" drop\n  }\n}\n")
	return b.String(), nil
}
