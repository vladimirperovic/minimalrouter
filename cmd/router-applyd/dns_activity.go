package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsactivity"
)

// DNS activity collection. routerd keeps the on/off setting and states it on
// every request over a dedicated socket; this helper converges dnsmasq on it
// by writing or removing one fixed drop-in file and restarting dnsmasq. While
// the drop-in is installed, dnsmasq appends one line per lookup to a tmpfs
// file. This helper drains that file every minute into a bounded in-memory
// table, then truncates it, so the file cannot grow while routerd is not
// collecting. Only validated (client, name, count) tuples cross the privilege
// boundary; raw log lines never do. As soon as the drop-in is gone, the file
// and the in-memory table are discarded.
//
// The generated minimalrouter.conf and the canonical apply transaction are
// not involved: their packages are part of the byte-identical bootstrap
// contract (see internal/dnsactivity).

const (
	// dnsQueryLogMaxRead bounds the bytes parsed per drain. A minute of
	// lookups on a busy home network is well under 1 MiB; anything beyond the
	// bound is discarded with the truncation rather than parsed.
	dnsQueryLogMaxRead = 16 << 20
	// dnsPendingMaxEntries bounds distinct (client, name) pairs held between
	// drains. Lookups for new pairs beyond it are counted as dropped.
	dnsPendingMaxEntries = 8192
	// One response must stay well inside apply.MaxResponseBytes.
	dnsBatchMaxEntries = 1000
	dnsBatchMaxBytes   = 160 << 10
	dnsDrainInterval   = time.Minute
)

var (
	dnsDropInPath   = dnsactivity.DropInPath
	dnsQueryLogPath = dnsactivity.QueryLogPath
	// Replaced in tests; production runs only these fixed argument vectors.
	testDnsmasqConfig = func() error { return runFixed("/usr/sbin/dnsmasq", "--test") }
	restartDnsmasq    = func() error { return runFixed("/sbin/rc-service", "dnsmasq", "restart") }
)

type dnsLookupKey struct {
	client string
	name   string
}

var dnsActivity struct {
	sync.Mutex
	pending map[dnsLookupKey]*dnsactivity.Lookup
	dropped uint64
}

// runDNSActivityCollector drains the query log on a fixed interval so its size
// stays bounded even when routerd is stopped or not asking for batches.
func runDNSActivityCollector() {
	ticker := time.NewTicker(dnsDrainInterval)
	defer ticker.Stop()
	for {
		collectDNSQueryLog(time.Now())
		<-ticker.C
	}
}

// dnsQueryLoggingEnabled reports whether the installed drop-in makes dnsmasq
// log queries to the tmpfs file.
func dnsQueryLoggingEnabled() bool {
	data, err := os.ReadFile(dnsDropInPath)
	if err != nil {
		return false
	}
	directive := "log-facility=" + dnsQueryLogPath
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == directive {
			return true
		}
	}
	return false
}

// ensureDNSQueryLogging converges the drop-in on the requested state. A
// change restarts dnsmasq under applyMu, so it never interleaves with an apply
// transaction. Enabling is fail-closed: if dnsmasq rejects the configuration
// or does not come back, the drop-in is removed and dnsmasq restarted without
// it, so DNS service never depends on statistics.
func ensureDNSQueryLogging(enabled bool) error {
	want := []byte(dnsactivity.DropInFor(dnsQueryLogPath))
	current, err := os.ReadFile(dnsDropInPath)
	installed := err == nil
	if enabled && installed && bytes.Equal(current, want) {
		return nil
	}
	if !enabled && !installed {
		return nil
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if !enabled {
		if err := os.Remove(dnsDropInPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove DNS activity drop-in: %w", err)
		}
		if err := restartDnsmasq(); err != nil {
			return fmt.Errorf("restart dnsmasq without query logging: %w", err)
		}
		log.Printf("[DNS] query logging disabled")
		return nil
	}
	if err := atomicWrite(dnsDropInPath, want, 0640); err != nil {
		return fmt.Errorf("write DNS activity drop-in: %w", err)
	}
	if err := testDnsmasqConfig(); err != nil {
		_ = os.Remove(dnsDropInPath)
		return fmt.Errorf("dnsmasq rejected query logging: %w", err)
	}
	if err := restartDnsmasq(); err != nil {
		_ = os.Remove(dnsDropInPath)
		if restoreErr := restartDnsmasq(); restoreErr != nil {
			log.Printf("[DNS] dnsmasq restart without query logging also failed: %v", restoreErr)
		}
		return fmt.Errorf("restart dnsmasq with query logging: %w", err)
	}
	log.Printf("[DNS] query logging enabled")
	return nil
}

func collectDNSQueryLog(now time.Time) {
	dnsActivity.Lock()
	defer dnsActivity.Unlock()
	if !dnsQueryLoggingEnabled() {
		dnsActivity.pending = nil
		dnsActivity.dropped = 0
		if err := os.Remove(dnsQueryLogPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[DNS] could not remove query log after logging was disabled: %v", err)
		}
		return
	}
	file, err := os.OpenFile(dnsQueryLogPath, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("[DNS] could not open query log: %v", err)
		}
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		log.Printf("[DNS] query log is not a regular file; ignoring it")
		return
	}
	if info.Size() == 0 {
		return
	}
	readDNSQueryLines(io.LimitReader(file, dnsQueryLogMaxRead), now)
	// Lines dnsmasq appends between the last read and this truncation are
	// lost. That window is microseconds once a minute, which statistics can
	// tolerate; rotating instead would need dnsmasq to recreate the file
	// after it has dropped root.
	if err := file.Truncate(0); err != nil {
		log.Printf("[DNS] could not truncate query log: %v", err)
	}
}

func readDNSQueryLines(source io.Reader, now time.Time) {
	reader := bufio.NewReaderSize(source, 4096)
	skipping := false
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// An overlong line is never a dnsmasq query record; skip to its end.
			skipping = true
			continue
		}
		if err != nil {
			// EOF: a final line without a newline is still being written and
			// is discarded with the truncation.
			return
		}
		if skipping {
			skipping = false
			continue
		}
		if client, name, ok := parseDNSQueryLine(string(line)); ok {
			recordDNSLookup(client, name, now)
		}
	}
}

func recordDNSLookup(client, name string, now time.Time) {
	if dnsActivity.pending == nil {
		dnsActivity.pending = map[dnsLookupKey]*dnsactivity.Lookup{}
	}
	key := dnsLookupKey{client: client, name: name}
	if entry := dnsActivity.pending[key]; entry != nil {
		if entry.Count < math.MaxUint32 {
			entry.Count++
		}
		entry.LastSeen = now.Unix()
		return
	}
	if len(dnsActivity.pending) >= dnsPendingMaxEntries {
		dnsActivity.dropped++
		return
	}
	dnsActivity.pending[key] = &dnsactivity.Lookup{Client: client, Name: name, Count: 1, LastSeen: now.Unix()}
}

// parseDNSQueryLine accepts exactly the dnsmasq log-queries record
//
//	Oct  6 12:00:00 dnsmasq[1234]: query[A] example.com from 192.168.1.50
//
// and returns the normalized client address and query name. Everything else
// dnsmasq logs to the same file (forwarded, reply, cached, DHCP, startup) is
// ignored. Reverse lookups and loopback clients (the router itself) are not
// device activity.
func parseDNSQueryLine(line string) (string, string, bool) {
	const marker = ": query["
	line = strings.TrimRight(line, "\r\n")
	index := strings.Index(line, marker)
	if index < 0 {
		return "", "", false
	}
	prefix := line[:index]
	if !strings.HasSuffix(prefix, "]") || !strings.Contains(prefix, "dnsmasq[") {
		return "", "", false
	}
	rest := line[index+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end <= 0 || end > 16 {
		return "", "", false
	}
	if rest[:end] == "PTR" {
		return "", "", false
	}
	fields := strings.Fields(rest[end+1:])
	if len(fields) != 3 || fields[1] != "from" {
		return "", "", false
	}
	name, ok := normalizeDNSQueryName(fields[0])
	if !ok {
		return "", "", false
	}
	address, err := netip.ParseAddr(fields[2])
	if err != nil {
		return "", "", false
	}
	address = address.Unmap().WithZone("")
	if address.IsLoopback() || address.IsUnspecified() {
		return "", "", false
	}
	return address.String(), name, true
}

// normalizeDNSQueryName lowercases a query name and accepts only multi-label
// hostnames made of letters, digits, hyphens and underscores. Single-label
// names (wpad, localhost) and reverse-lookup zones are not sites.
func normalizeDNSQueryName(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(name) == 0 || len(name) > 253 {
		return "", false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 || labels[len(labels)-1] == "arpa" {
		return "", false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return "", false
			}
		}
	}
	return name, true
}

// dnsActivityDrain converges query logging on the requested state and returns
// one bounded page of collected lookups, removing them from memory. It first
// picks up anything logged since the last tick so a collection round sees
// current data.
func dnsActivityDrain(request dnsactivity.Request) dnsactivity.Response {
	if err := ensureDNSQueryLogging(request.Enabled); err != nil {
		log.Printf("[DNS] %v", err)
		return dnsactivity.Response{Error: "DNS query logging could not be changed"}
	}
	collectDNSQueryLog(time.Now())
	dnsActivity.Lock()
	defer dnsActivity.Unlock()
	response := dnsactivity.Response{Success: true, Enabled: dnsQueryLoggingEnabled(), Entries: []dnsactivity.Lookup{}}
	size := 0
	for key, entry := range dnsActivity.pending {
		cost := len(entry.Client) + len(entry.Name) + 64
		if len(response.Entries) >= dnsBatchMaxEntries || size+cost > dnsBatchMaxBytes {
			response.More = true
			break
		}
		response.Entries = append(response.Entries, *entry)
		size += cost
		delete(dnsActivity.pending, key)
	}
	response.Dropped = dnsActivity.dropped
	dnsActivity.dropped = 0
	return response
}

// startDNSActivityListener serves the DNS activity socket once the apply
// socket exists, with the same peer and permission checks.
func startDNSActivityListener() {
	<-runtimeAdmissionReady
	for {
		if _, err := os.Stat(socketDir); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(dnsactivity.SocketPath)
	listener, err := net.Listen("unix", dnsactivity.SocketPath)
	if err != nil {
		log.Printf("DNS activity socket unavailable: %v", err)
		return
	}
	defer listener.Close()
	if err := secureSocketForRouterd(dnsactivity.SocketPath); err != nil {
		log.Printf("cannot secure DNS activity socket: %v", err)
		_ = os.Remove(dnsactivity.SocketPath)
		return
	}
	log.Printf("router-applyd DNS activity listening on unix://%s", dnsactivity.SocketPath)
	// The shared acceptor bounds concurrent peers before Accept.
	serveServiceActionConnections(listener, handleDNSActivityConnection)
}

func handleDNSActivityConnection(conn net.Conn) {
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(requestReadTimeout)); err != nil {
		return
	}
	var response dnsactivity.Response
	if err := validatePeer(conn); err != nil {
		response = dnsactivity.Response{Error: "unauthorized local peer"}
	} else if request, err := dnsactivity.DecodeRequest(conn); err != nil {
		response = dnsactivity.Response{Error: err.Error()}
	} else {
		response = dnsActivityDrain(request)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(responseWriteTimeout)); err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(&response)
}
