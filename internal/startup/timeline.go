package startup

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Window         = 10 * time.Minute
	CheckInterval  = 1 * time.Second
	SampleInterval = 1 * time.Second
	MaxBoots       = 5

	// samplePersistInterval bounds how often the resource series reaches disk.
	// Every write serialises the whole boot document, so persisting each sample
	// made the bytes written grow with the square of the sample count: a full
	// ten-minute capture wrote 20 MiB to produce a 71 KiB file, on an appliance
	// that boots from flash. Samples are held in memory between flushes; a crash
	// loses at most this much of the series, while events, readiness milestones
	// and completion still persist at once.
	samplePersistInterval = 15 * time.Second
)

type Event struct {
	OffsetSeconds int64  `json:"offset_seconds"`
	Kind          string `json:"kind"`
	Message       string `json:"message"`
}

type Sample struct {
	OffsetSeconds int64   `json:"offset_seconds"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsedMB  float64 `json:"memory_used_mb"`
	MemoryTotalMB float64 `json:"memory_total_mb"`
}

type Readiness struct {
	ManagementSeconds *int64 `json:"management_seconds,omitempty"`
	PPPoESeconds      *int64 `json:"pppoe_seconds,omitempty"`
	DNSSeconds        *int64 `json:"dns_seconds,omitempty"`
	InternetSeconds   *int64 `json:"internet_seconds,omitempty"`
	WireGuardSeconds  *int64 `json:"wireguard_seconds,omitempty"`
}

type Boot struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Completed bool      `json:"completed"`
	Readiness Readiness `json:"readiness"`
	Events    []Event   `json:"events"`
	Samples   []Sample  `json:"samples"`
}

type Recorder struct {
	mu          sync.Mutex
	dir         string
	started     time.Time
	boot        Boot
	lastCPU     uint64
	lastTotal   uint64
	lastSampleP time.Time
}

func New(dataDir string) (*Recorder, error) {
	dir := filepath.Join(dataDir, "startup")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	bootID, startedAt := kernelBootIdentity(now)
	boot := Boot{ID: bootID, StartedAt: startedAt}
	path := filepath.Join(dir, bootID+".json")
	reused := false
	if data, err := os.ReadFile(path); err == nil {
		var existing Boot
		if json.Unmarshal(data, &existing) == nil && existing.ID == bootID && !existing.StartedAt.IsZero() {
			boot = existing
			startedAt = existing.StartedAt
			reused = true
		}
	}

	r := &Recorder{dir: dir, started: startedAt, boot: boot}
	if reused && r.boot.Completed {
		return r, nil
	}
	if reused {
		r.Event("routerd", "Management process restarted")
	} else {
		r.Event("routerd", "Management process started")
	}
	if err := r.persist(); err != nil {
		return nil, err
	}
	// The set of boot files only changes when a boot starts, so pruning belongs
	// here rather than inside every persist.
	_ = prune(dir)
	return r, nil
}

// kernelBootIdentity anchors the timeline to the actual Linux boot rather than
// the routerd process lifetime. A routerd restart therefore continues the same
// boot capture instead of creating a fake new boot. The timestamp fallback is
// used only on non-Linux/test environments where procfs is unavailable.
func kernelBootIdentity(now time.Time) (string, time.Time) {
	idBytes, idErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
	uptimeBytes, uptimeErr := os.ReadFile("/proc/uptime")
	if idErr == nil && uptimeErr == nil {
		id := strings.TrimSpace(string(idBytes))
		fields := strings.Fields(string(uptimeBytes))
		if validBootID(id) && len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil && seconds >= 0 {
				return id, now.Add(-time.Duration(seconds * float64(time.Second))).UTC()
			}
		}
	}
	return now.Format("20060102T150405.000000000Z"), now
}

func validBootID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func (r *Recorder) Event(kind, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.boot.Completed {
		return
	}
	r.boot.Events = append(r.boot.Events, Event{
		OffsetSeconds: r.offsetSeconds(),
		Kind:          kind,
		Message:       message,
	})
	_ = r.persistLocked()
}

func (r *Recorder) Ready(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.boot.Completed {
		return
	}
	seconds := r.offsetSeconds()
	changed := false
	setOnce := func(dst **int64) {
		if *dst == nil {
			value := seconds
			*dst = &value
			changed = true
		}
	}
	switch kind {
	case "management":
		setOnce(&r.boot.Readiness.ManagementSeconds)
	case "pppoe":
		setOnce(&r.boot.Readiness.PPPoESeconds)
	case "dns":
		setOnce(&r.boot.Readiness.DNSSeconds)
	case "internet":
		setOnce(&r.boot.Readiness.InternetSeconds)
	case "wireguard":
		setOnce(&r.boot.Readiness.WireGuardSeconds)
	}
	// Once a milestone is recorded, repeated checks must be read-only. The old
	// code rewrote the JSON file every 15 seconds even after every milestone was
	// already ready.
	if changed {
		_ = r.persistLocked()
	}
}

func (r *Recorder) offsetSeconds() int64 {
	seconds := int64(time.Since(r.started).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (r *Recorder) Run(ctx context.Context, pppoeEnabled bool, wgInterface string, wgEnabled bool) {
	r.mu.Lock()
	alreadyCompleted := r.boot.Completed
	r.mu.Unlock()
	if alreadyCompleted {
		return
	}

	remaining := time.Until(r.started.Add(Window))
	if remaining <= 0 {
		r.complete()
		return
	}

	checkTicker := time.NewTicker(CheckInterval)
	defer checkTicker.Stop()
	sampleTicker := time.NewTicker(SampleInterval)
	defer sampleTicker.Stop()
	deadline := time.NewTimer(remaining)
	defer deadline.Stop()

	// Say why the uplink milestones are absent, so a capture showing only
	// management is not read as a boot that failed to get online.
	if !pppoeEnabled {
		r.Event("wan", "No uplink configured at boot; DNS and Internet milestones do not apply")
	}
	// Record one lightweight local sample at process start. Network probes are
	// handled separately and stop permanently as soon as their milestone is met.
	r.sample()
	if r.checkReadiness(ctx, pppoeEnabled, wgInterface, wgEnabled) {
		r.complete()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			r.complete()
			return
		case <-sampleTicker.C:
			r.sample()
		case <-checkTicker.C:
			if r.checkReadiness(ctx, pppoeEnabled, wgInterface, wgEnabled) {
				r.complete()
				return
			}
		}
	}
}

// checkReadiness performs only checks that have not succeeded yet. DNS and
// Internet probes are deferred until PPPoE exists when PPPoE is configured, so
// first boot never burns timeout/CPU on probes that cannot possibly succeed.
// It returns true when every milestone expected by this configuration is ready.
func (r *Recorder) checkReadiness(ctx context.Context, pppoeEnabled bool, wgInterface string, wgEnabled bool) bool {
	r.mu.Lock()
	readiness := r.boot.Readiness
	r.mu.Unlock()

	if pppoeEnabled && readiness.PPPoESeconds == nil {
		if _, err := net.InterfaceByName("ppp0"); err == nil {
			r.Ready("pppoe")
			readiness.PPPoESeconds = int64Ptr(r.offsetSeconds())
		}
	}
	if wgEnabled && wgInterface != "" && readiness.WireGuardSeconds == nil {
		if _, err := net.InterfaceByName(wgInterface); err == nil {
			r.Ready("wireguard")
			readiness.WireGuardSeconds = int64Ptr(r.offsetSeconds())
		}
	}

	// With no uplink configured there is nothing for a resolver or an HTTPS
	// dial to reach, so those milestones do not apply to this boot. The old
	// code deferred them only while PPPoE was configured but still coming up,
	// which left the case the comment names -- first boot, before the wizard --
	// probing for the full ten-minute window and never completing.
	if !pppoeEnabled {
		managementReady := readiness.ManagementSeconds != nil
		wgReady := !wgEnabled || wgInterface == "" || readiness.WireGuardSeconds != nil
		return managementReady && wgReady
	}

	wanReady := readiness.PPPoESeconds != nil
	if wanReady && readiness.DNSSeconds == nil {
		dnsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := net.DefaultResolver.LookupHost(dnsCtx, "example.com")
		cancel()
		if err == nil {
			r.Ready("dns")
			readiness.DNSSeconds = int64Ptr(r.offsetSeconds())
		}
	}
	if wanReady && readiness.InternetSeconds == nil {
		internetCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := (&net.Dialer{}).DialContext(internetCtx, "tcp", "1.1.1.1:443")
		cancel()
		if err == nil {
			_ = conn.Close()
			r.Ready("internet")
			readiness.InternetSeconds = int64Ptr(r.offsetSeconds())
		}
	}

	managementReady := readiness.ManagementSeconds != nil
	wgReady := !wgEnabled || wgInterface == "" || readiness.WireGuardSeconds != nil
	return managementReady && readiness.PPPoESeconds != nil && wgReady &&
		readiness.DNSSeconds != nil && readiness.InternetSeconds != nil
}

func int64Ptr(value int64) *int64 { return &value }

func (r *Recorder) complete() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.boot.Completed {
		return
	}
	r.boot.Completed = true
	_ = r.persistLocked()
}

func (r *Recorder) sample() {
	total, idle := readCPU()
	used, totalMB := readMemory()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.boot.Completed {
		return
	}
	cpu := 0.0
	if r.lastTotal > 0 && total > r.lastTotal {
		deltaTotal := total - r.lastTotal
		deltaIdle := idle - r.lastCPU
		cpu = 100 * (1 - float64(deltaIdle)/float64(deltaTotal))
		if cpu < 0 {
			cpu = 0
		}
		if cpu > 100 {
			cpu = 100
		}
	}
	r.lastTotal = total
	r.lastCPU = idle
	r.boot.Samples = append(r.boot.Samples, Sample{
		OffsetSeconds: r.offsetSeconds(),
		CPUPercent:    cpu,
		MemoryUsedMB:  used,
		MemoryTotalMB: totalMB,
	})
	now := time.Now()
	if now.Sub(r.lastSampleP) < samplePersistInterval {
		return
	}
	r.lastSampleP = now
	_ = r.persistLocked()
}

func readCPU() (uint64, uint64) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 {
		return 0, 0
	}
	var total uint64
	for _, value := range fields[1:] {
		number, _ := strconv.ParseUint(value, 10, 64)
		total += number
	}
	idle, _ := strconv.ParseUint(fields[4], 10, 64)
	return total, idle
}

func readMemory() (float64, float64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var total, available float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			total = value / 1024
		case "MemAvailable:":
			available = value / 1024
		}
	}
	return total - available, total
}

func (r *Recorder) persist() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
}

func (r *Recorder) persistLocked() error {
	data, err := json.MarshalIndent(r.boot, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.dir, r.boot.ID+".json.tmp")
	dst := filepath.Join(r.dir, r.boot.ID+".json")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

type bootFile struct {
	name    string
	modTime time.Time
}

func prune(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]bootFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, bootFile{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(files) <= MaxBoots {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files[:len(files)-MaxBoots] {
		_ = os.Remove(filepath.Join(dir, file.name))
	}
	return nil
}

func Load(dataDir string) ([]Boot, error) {
	dir := filepath.Join(dataDir, "startup")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Boot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Boot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var boot Boot
		if json.Unmarshal(data, &boot) == nil {
			out = append(out, boot)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > MaxBoots {
		out = out[:MaxBoots]
	}
	return out, nil
}

func (b Boot) Summary() string {
	return fmt.Sprintf("boot %s", b.StartedAt.Format(time.RFC3339))
}
