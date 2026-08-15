package startup

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Window         = 10 * time.Minute
	SampleInterval = 15 * time.Second
	MaxBoots       = 5
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
	mu        sync.Mutex
	dir       string
	started   time.Time
	boot      Boot
	lastCPU   uint64
	lastTotal uint64
}

func New(dir string) (*Recorder, error) {
	dir = filepath.Join(dir, "startup")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	r := &Recorder{
		dir:     dir,
		started: now,
		boot: Boot{
			ID:        now.Format("20060102T150405.000000000Z"),
			StartedAt: now,
		},
	}
	r.Event("routerd", "Management process started")
	if err := r.persist(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) Event(kind, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.boot.Events = append(r.boot.Events, Event{
		OffsetSeconds: int64(time.Since(r.started).Seconds()),
		Kind:          kind,
		Message:       message,
	})
	_ = r.persistLocked()
}

func (r *Recorder) Ready(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seconds := int64(time.Since(r.started).Seconds())
	setOnce := func(dst **int64) {
		if *dst == nil {
			value := seconds
			*dst = &value
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
	_ = r.persistLocked()
}

func (r *Recorder) Run(ctx context.Context, pppoeEnabled bool, wgInterface string, wgEnabled bool) {
	ticker := time.NewTicker(SampleInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(Window)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			r.mu.Lock()
			r.boot.Completed = true
			_ = r.persistLocked()
			r.mu.Unlock()
			return
		case <-ticker.C:
			r.sample()
			if pppoeEnabled {
				if _, err := net.InterfaceByName("ppp0"); err == nil {
					r.Ready("pppoe")
				}
			}
			if wgEnabled && wgInterface != "" {
				if _, err := net.InterfaceByName(wgInterface); err == nil {
					r.Ready("wireguard")
				}
			}
			dnsCtx, cancelDNS := context.WithTimeout(ctx, 2*time.Second)
			if _, err := net.DefaultResolver.LookupHost(dnsCtx, "example.com"); err == nil {
				r.Ready("dns")
			}
			cancelDNS()

			internetCtx, cancelInternet := context.WithTimeout(ctx, 2*time.Second)
			dialer := net.Dialer{}
			if conn, err := dialer.DialContext(internetCtx, "tcp", "1.1.1.1:443"); err == nil {
				_ = conn.Close()
				r.Ready("internet")
			}
			cancelInternet()
		}
	}
}

func (r *Recorder) sample() {
	total, idle := readCPU()
	used, totalMB := readMemory()

	r.mu.Lock()
	defer r.mu.Unlock()
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
		OffsetSeconds: int64(time.Since(r.started).Seconds()),
		CPUPercent:    cpu,
		MemoryUsedMB:  used,
		MemoryTotalMB: totalMB,
	})
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
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return prune(r.dir)
}

func prune(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	if len(names) <= MaxBoots {
		return nil
	}
	for _, name := range names[:len(names)-MaxBoots] {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

func Load(dir string) ([]Boot, error) {
	dir = filepath.Join(dir, "startup")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Boot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Boot, 0, MaxBoots)
	for i := len(entries) - 1; i >= 0 && len(out) < MaxBoots; i-- {
		entry := entries[i]
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
	return out, nil
}

func (b Boot) Summary() string {
	return fmt.Sprintf("boot %s", b.StartedAt.Format(time.RFC3339))
}
