package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"golang.org/x/net/dns/dnsmessage"
)

const filterHeader = "# minimalrouter-filter-v1 "
const filterHealthName = "filter-health.minimalrouter.invalid."

var filterPath = dnsfilter.DropInPath
var filterUpdateMu sync.Mutex
var verifyFilterResolver = filterResolverHealthy
var guardFilterMutation = guardServiceActionMutation
var checkFilterResources = filterResourceBudget

func startDNSFilterListener() {
	<-runtimeAdmissionReady
	_ = os.Remove(dnsfilter.SocketPath)
	listener, err := net.Listen("unix", dnsfilter.SocketPath)
	if err != nil {
		log.Printf("[DNS FILTER] listener unavailable: %v", err)
		return
	}
	defer listener.Close()
	if err = secureSocketForRouterd(dnsfilter.SocketPath); err != nil {
		log.Printf("[DNS FILTER] socket permissions: %v", err)
		_ = os.Remove(dnsfilter.SocketPath)
		return
	}
	serveServiceActionConnections(listener, handleDNSFilterConnection)
}

func readFilterState() (dnsfilter.Applied, error) {
	f, err := os.Open(filterPath)
	if errors.Is(err, os.ErrNotExist) {
		return dnsfilter.Applied{Policy: dnsfilter.DefaultPolicy(), Sources: map[string]string{}}, nil
	}
	if err != nil {
		return dnsfilter.Applied{}, err
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(f, dnsfilter.MaxMetadataBytes+1), dnsfilter.MaxMetadataBytes+1).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, filterHeader) {
		return dnsfilter.Applied{}, errors.New("DNS filter state is invalid")
	}
	var state dnsfilter.Applied
	if err = dnsfilter.DecodeMetadata([]byte(strings.TrimPrefix(line, filterHeader)), &state); err != nil {
		return state, err
	}
	return state, state.Policy.Validate()
}

func handleDNSFilterConnection(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	response := dnsfilter.Response{}
	if err := validatePeer(conn); err != nil {
		response.Error = "unauthorized local peer"
	} else {
		reader := bufio.NewReaderSize(conn, dnsfilter.MaxMetadataBytes+1)
		line, err := reader.ReadSlice('\n')
		var request dnsfilter.Request
		if err != nil || dnsfilter.DecodeMetadata(line, &request) != nil || request.Version != 1 {
			response.Error = "invalid DNS filter request"
		} else if request.Operation == "status" {
			if _, err := reader.ReadByte(); err != io.EOF {
				response.Error = "unexpected status data"
			} else {
				applyMu.Lock()
				response.State, err = readFilterState()
				if err == nil {
					response.State.Healthy = response.State.AppliedAt > 0 && verifyFilterResolver(response.State.Policy.Revision) == nil
				}
				applyMu.Unlock()
				if err != nil {
					response.Error = "DNS filter state unavailable"
				}
			}
		} else if request.Operation == "apply" {
			if !filterUpdateMu.TryLock() {
				response.Error = "DNS filter update is already running"
			} else {
				response.State, err = applyDNSFilter(request, reader)
				filterUpdateMu.Unlock()
				if err != nil {
					response.Error = err.Error()
				}
			}
		} else {
			response.Error = "unsupported DNS filter operation"
		}
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func applyDNSFilter(request dnsfilter.Request, domains io.Reader) (dnsfilter.Applied, error) {
	if err := request.Policy.Validate(); err != nil {
		return dnsfilter.Applied{}, err
	}
	if len(request.Sources) > 4 {
		return dnsfilter.Applied{}, errors.New("invalid source manifest")
	}
	for id, hash := range request.Sources {
		if !request.Policy.Categories[id] || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return dnsfilter.Applied{}, errors.New("invalid source hash")
		}
	}
	for id, on := range request.Policy.Categories {
		if on && request.Sources[id] == "" {
			return dnsfilter.Applied{}, errors.New("enabled category has no validated list")
		}
	}
	state := dnsfilter.Applied{Policy: request.Policy, AppliedAt: time.Now().Unix(), Sources: request.Sources}
	if request.ExpectedRevision == ^uint64(0) {
		return state, errors.New("DNS filter revision exhausted")
	}
	state.Policy.Revision = request.ExpectedRevision + 1
	// Build in a fixed root-owned directory. No caller-selected paths or config
	// snippets can reach dnsmasq. The current file survives partial uploads.
	tmp, err := os.CreateTemp(filepath.Dir(filterPath), ".dns-filter-*")
	if err != nil {
		return state, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	defer tmp.Close()
	if err = tmp.Chmod(0640); err != nil {
		return state, err
	}
	// The final header is written separately after the streaming count is known.
	content, err := os.CreateTemp(filepath.Dir(filterPath), ".dns-domains-*")
	if err != nil {
		return state, err
	}
	defer os.Remove(content.Name())
	defer content.Close()
	w := bufio.NewWriterSize(content, 32768)
	limited := &io.LimitedReader{R: domains, N: dnsfilter.MaxStreamBytes + 1}
	scan := bufio.NewScanner(limited)
	scan.Buffer(make([]byte, 512), 512)
	for scan.Scan() {
		d, ok := dnsfilter.Domain(scan.Text())
		if !ok || d != scan.Text() {
			return state, errors.New("invalid blocking domain")
		}
		state.Domains++
		if state.Domains > dnsfilter.MaxDomains {
			return state, errors.New("DNS blocklist exceeds appliance limit")
		}
		if !state.Policy.Allowed(d) {
			if _, err = fmt.Fprintf(w, "address=/%s/\n", d); err != nil {
				return state, err
			}
		}
	}
	if err = scan.Err(); err != nil {
		return state, err
	}
	if limited.N <= 0 {
		return state, errors.New("DNS domain stream too large")
	}
	if state.Domains == 0 && len(state.Sources) > 0 {
		return state, errors.New("empty blocking list refused")
	}
	if state.Domains > 0 && len(state.Sources) == 0 {
		return state, errors.New("blocking domains require an enabled source")
	}
	if err = checkFilterResources(state.Domains); err != nil {
		return state, err
	}
	for _, exception := range state.Policy.Exceptions {
		if _, err = fmt.Fprintf(w, "server=/%s/#\n", exception.Domain); err != nil {
			return state, err
		}
	}
	if _, err = fmt.Fprintf(w, "txt-record=%s,%d\n", strings.TrimSuffix(filterHealthName, "."), state.Policy.Revision); err != nil {
		return state, err
	}
	if err = w.Flush(); err != nil {
		return state, err
	}
	metadata, err := json.Marshal(state)
	if err != nil {
		return state, err
	}
	if _, err = fmt.Fprintf(tmp, "%s%s\n", filterHeader, metadata); err != nil {
		return state, err
	}
	if _, err = content.Seek(0, io.SeekStart); err != nil {
		return state, err
	}
	if _, err = io.Copy(tmp, content); err != nil {
		return state, err
	}
	if err = tmp.Sync(); err != nil {
		return state, err
	}
	if err = tmp.Close(); err != nil {
		return state, err
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if err := guardFilterMutation(); err != nil {
		return state, errors.New("confirm or roll back the pending network change before changing DNS protection")
	}
	previous, err := readFilterState()
	if err != nil {
		return state, err
	}
	if previous.Policy.Revision != request.ExpectedRevision {
		return previous, errors.New("DNS filter changed in another session; reload before saving")
	}
	backup := filterPath + ".previous"
	hadPrevious := previous.AppliedAt > 0
	if _, err := os.Stat(filterPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
		return previous, errors.New("unfinished DNS filter activation requires helper recovery")
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return previous, err
	}
	if hadPrevious {
		if err = os.Link(filterPath, backup); err != nil {
			return previous, err
		}
	}
	if err = syncFilterDirectory(); err != nil {
		return previous, err
	}
	if err = atomicWrite(filterPath+".pending", []byte(strconv.FormatBool(hadPrevious)), 0600); err != nil {
		return previous, err
	}
	if err = os.Rename(name, filterPath); err != nil {
		return previous, err
	}
	if err = syncFilterDirectory(); err == nil {
		err = testDnsmasqConfig()
	}
	if err == nil {
		err = restartDnsmasq()
	}
	if err == nil {
		err = verifyFilterResolver(state.Policy.Revision)
	}
	if err != nil {
		rollbackErr := restoreFilterPrevious(hadPrevious)
		if rollbackErr == nil {
			rollbackErr = restartDnsmasq()
		}
		if rollbackErr == nil && hadPrevious {
			rollbackErr = verifyFilterResolver(previous.Policy.Revision)
		}
		if rollbackErr != nil {
			return previous, errors.New("DNS filter activation and rollback verification failed; console recovery required")
		}
		if rollbackErr = finishFilterActivation(); rollbackErr != nil {
			return previous, rollbackErr
		}
		return previous, errors.New("DNS filter activation failed; previous DNS configuration restored")
	}
	if err = finishFilterActivation(); err != nil {
		return state, errors.New("DNS policy is active but its commit could not be made durable; helper recovery required")
	}
	state.Healthy = true
	return state, nil
}

func restoreFilterPrevious(existed bool) error {
	if existed {
		staged := filterPath + ".restore"
		if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Link(filterPath+".previous", staged); err != nil {
			return err
		}
		if err := os.Rename(staged, filterPath); err != nil {
			return err
		}
	} else if err := os.Remove(filterPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncFilterDirectory()
}

func finishFilterActivation() error {
	if err := os.Remove(filterPath + ".pending"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncFilterDirectory(); err != nil {
		return err
	}
	// An orphaned hard link after a crash is harmless; the marker is already
	// durably gone. Keep the rollback copy until that point.
	_ = os.Remove(filterPath + ".previous")
	return nil
}

func recoverFilterActivation() error {
	data, err := os.ReadFile(filterPath + ".pending")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	existed, err := strconv.ParseBool(string(data))
	if err != nil {
		return err
	}
	if err = restoreFilterPrevious(existed); err != nil {
		return err
	}
	if err = restartDnsmasq(); err != nil {
		return err
	}
	if existed {
		state, err := readFilterState()
		if err != nil {
			return err
		}
		if err = verifyFilterResolver(state.Policy.Revision); err != nil {
			return err
		}
	}
	return finishFilterActivation()
}

func syncFilterDirectory() error {
	d, err := os.Open(filepath.Dir(filterPath))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Conservative preflight includes headroom for parsing the new list while the
// old resolver still runs. Refuse growth instead of risking an appliance OOM.
func filterResourceBudget(domains int) error {
	if domains == 0 {
		return nil
	}
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return errors.New("available memory could not be checked")
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) == 3 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || kb < (64<<10)+uint64(domains)/4 {
				return errors.New("insufficient available memory for selected DNS lists; disable a category or increase appliance RAM")
			}
			return nil
		}
	}
	return errors.New("available memory could not be checked")
}

// A local TXT marker verifies which generation the running resolver loaded.
// No WAN lookup is needed, and an offline WAN does not invalidate blocking.
func filterResolverHealthy(revision uint64) error {
	return filterResolverHealthyAt(revision, "127.0.0.1:53")
}

func filterResolverHealthyAt(revision uint64, address string) error {
	conn, err := net.DialTimeout("udp", address, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	name, _ := dnsmessage.NewName(filterHealthName)
	msg := dnsmessage.Message{Header: dnsmessage.Header{ID: 23719}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET}}}
	packet, _ := msg.Pack()
	if _, err = conn.Write(packet); err != nil {
		return err
	}
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return err
	}
	if err = msg.Unpack(buffer[:n]); err != nil {
		return err
	}
	if msg.ID != 23719 || !msg.Response || msg.RCode != dnsmessage.RCodeSuccess {
		return errors.New("DNS filter generation probe failed")
	}
	for _, answer := range msg.Answers {
		if txt, ok := answer.Body.(*dnsmessage.TXTResource); ok && answer.Header.Name == name && strings.Join(txt.TXT, "") == strconv.FormatUint(revision, 10) {
			return nil
		}
	}
	return errors.New("running DNS generation does not match saved policy")
}
