//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/services"
	"golang.org/x/net/dns/dnsmessage"
)

func filterTestHooks(t *testing.T) {
	t.Helper()
	oldPath, oldTest, oldRestart, oldVerify, oldGuard, oldResources := filterPath, testDnsmasqConfig, restartDnsmasq, verifyFilterResolver, guardFilterMutation, checkFilterResources
	t.Cleanup(func() {
		filterPath, testDnsmasqConfig, restartDnsmasq, verifyFilterResolver, guardFilterMutation, checkFilterResources = oldPath, oldTest, oldRestart, oldVerify, oldGuard, oldResources
	})
	filterPath = filepath.Join(t.TempDir(), "filter.conf")
	testDnsmasqConfig = func() error { return nil }
	restartDnsmasq = func() error { return nil }
	verifyFilterResolver = func(uint64) error { return nil }
	guardFilterMutation = func() error { return nil }
	checkFilterResources = func(int) error { return nil }
}

func filterRequest(revision uint64) dnsfilter.Request {
	p := dnsfilter.DefaultPolicy()
	p.Categories["ads"] = true
	return dnsfilter.Request{Operation: "apply", ExpectedRevision: revision, Policy: p, Sources: map[string]string{"ads": strings.Repeat("a", 64)}}
}

func TestDNSFilterRejectsInjectionPreservesOldAndRecoversInterruptedActivation(t *testing.T) {
	filterTestHooks(t)
	request := filterRequest(0)
	for _, raw := range []string{"ads.example.com\nserver=/#/192.0.2.1\n", "ads.example.com/0.0.0.0\n", ""} {
		if _, err := applyDNSFilter(request, strings.NewReader(raw)); err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
	state, err := applyDNSFilter(request, strings.NewReader("ads.example.com\n"))
	if err != nil || !state.Healthy {
		t.Fatalf("apply: %+v %v", state, err)
	}
	old, err := os.ReadFile(filterPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyDNSFilter(request, strings.NewReader("other.example.com\n")); err == nil {
		t.Fatal("stale revision accepted")
	}
	testDnsmasqConfig = func() error { return errors.New("bad candidate") }
	if _, err = applyDNSFilter(filterRequest(1), strings.NewReader("new.example.com\n")); err == nil {
		t.Fatal("failed resolver activation accepted")
	}
	current, _ := os.ReadFile(filterPath)
	if !bytes.Equal(old, current) {
		t.Fatal("failed activation lost previous file")
	}
	if err = os.Link(filterPath, filterPath+".previous"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filterPath+".pending", []byte("true"), 0600); err != nil {
		t.Fatal(err)
	}
	// Rename a different inode over the current file, as activation does.
	if err = os.WriteFile(filterPath+".next", []byte("invalid candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filterPath+".next", filterPath); err != nil {
		t.Fatal(err)
	}
	if err = recoverFilterActivation(); err != nil {
		t.Fatal(err)
	}
	current, _ = os.ReadFile(filterPath)
	if !bytes.Equal(old, current) {
		t.Fatal("crash recovery did not restore last verified list")
	}
	if err = recoverFilterActivation(); err != nil {
		t.Fatal("recovery was not idempotent")
	}
}

func TestDNSFilterExceptionAndGuard(t *testing.T) {
	filterTestHooks(t)
	request := filterRequest(0)
	request.Policy.Exceptions = []dnsfilter.Exception{{Domain: "school.example.com"}}
	guardFilterMutation = func() error { return errors.New("pending confirmation") }
	if _, err := applyDNSFilter(request, strings.NewReader("example.com\nschool.example.com\ncdn.school.example.com\n")); err == nil {
		t.Fatal("pending network transaction ignored")
	}
	guardFilterMutation = func() error { return nil }
	if _, err := applyDNSFilter(request, strings.NewReader("example.com\nschool.example.com\ncdn.school.example.com\n")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filterPath)
	if bytes.Contains(raw, []byte("address=/school.example.com/")) || bytes.Contains(raw, []byte("address=/cdn.school.example.com/")) || !bytes.Contains(raw, []byte("server=/school.example.com/#")) {
		t.Fatal("subtree exception does not override nested list entries")
	}
}

func dnsTestQuery(t *testing.T, address, name string, kind dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	conn, err := net.DialTimeout("udp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	n, _ := dnsmessage.NewName(name + ".")
	message := dnsmessage.Message{Header: dnsmessage.Header{ID: 77, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: n, Type: kind, Class: dnsmessage.ClassINET}}}
	raw, _ := message.Pack()
	if _, err = conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	size, err := conn.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if err = message.Unpack(buffer[:size]); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestDNSFilterLiveResolverRecordTypesAndExceptions(t *testing.T) {
	if os.Getenv("MINIMALROUTER_DNS_FILTER_LAB") != "1" {
		t.Skip("requires isolated Linux DNS laboratory")
	}
	binary, err := exec.LookPath("dnsmasq")
	if err != nil {
		t.Fatal(err)
	}
	filterTestHooks(t)
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	listener.Close()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upstream.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, peer, err := upstream.ReadFrom(buffer)
			if err != nil {
				return
			}
			var reply dnsmessage.Message
			if reply.Unpack(buffer[:n]) != nil || len(reply.Questions) != 1 {
				continue
			}
			reply.Response = true
			reply.RecursionAvailable = true
			q := reply.Questions[0]
			if q.Type == dnsmessage.TypeA {
				reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 8}}}}
			}
			raw, err := reply.Pack()
			if err == nil {
				_, _ = upstream.WriteTo(raw, peer)
			}
		}
	}()
	var process *exec.Cmd
	var output bytes.Buffer
	stop := func() {
		if process != nil {
			_ = process.Process.Kill()
			_ = process.Wait()
			process = nil
		}
	}
	t.Cleanup(stop)
	base := filepath.Join(filepath.Dir(filterPath), "dnsmasq-base.txt")
	text := fmt.Sprintf("no-resolv\nno-hosts\nbind-interfaces\nlisten-address=127.0.0.1\nport=%d\nconf-file=%s\nserver=127.0.0.1#%d\nhost-record=local.example,192.0.2.7\n", port, filterPath, upstream.LocalAddr().(*net.UDPAddr).Port)
	for _, domain := range services.BuiltinBlocklist() {
		text += "address=/" + domain + "/\n"
	}
	if err = os.WriteFile(base, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	testDnsmasqConfig = func() error { cmd := exec.Command(binary, "--test", "--conf-file="+base); return cmd.Run() }
	restartDnsmasq = func() error {
		stop()
		output.Reset()
		process = exec.Command(binary, "--keep-in-foreground", "--conf-file="+base, "--pid-file=")
		process.Stdout = &output
		process.Stderr = &output
		if err := process.Start(); err != nil {
			return err
		}
		return nil
	}
	verifyFilterResolver = func(revision uint64) error {
		var last error
		for i := 0; i < 30; i++ {
			last = filterResolverHealthyAt(revision, address)
			if last == nil {
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return last
	}
	request := filterRequest(0)
	request.Policy.Exceptions = []dnsfilter.Exception{{Domain: "allowed.blocked.example"}, {Domain: "google.com"}}
	if _, err = applyDNSFilter(request, strings.NewReader("blocked.example\nallowed.blocked.example\ncdn.allowed.blocked.example\nlocal.example\n")); err != nil {
		stop()
		t.Fatalf("activate live resolver: %v %s", err, output.String())
	}
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA, dnsmessage.TypeCNAME, dnsmessage.Type(64), dnsmessage.Type(65)} {
		for _, domain := range []string{"blocked.example", "child.blocked.example", "ads.tiktok.com"} {
			reply := dnsTestQuery(t, address, domain, kind)
			if reply.RCode != dnsmessage.RCodeNameError {
				t.Fatalf("%s type %d escaped block: %v", domain, kind, reply.RCode)
			}
		}
	}
	for _, domain := range []string{"allowed.blocked.example", "cdn.allowed.blocked.example", "local.example", "unlisted.example", "adservice.google.com", "child.adservice.google.com"} {
		reply := dnsTestQuery(t, address, domain, dnsmessage.TypeA)
		if reply.RCode != dnsmessage.RCodeSuccess || len(reply.Answers) == 0 {
			t.Fatalf("allowed/local name %s failed: %+v", domain, reply)
		}
	}
	if err = filterResolverHealthyAt(99, address); err == nil {
		t.Fatal("wrong DNS generation accepted")
	}
	// Log the actual memory footprint for this bounded fixture; do not confuse
	// it with the cost of a full production catalog.
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", process.Process.Pid)); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				t.Log(line)
			}
		}
	}
}

func TestServiceDestinationsLiveAtomicReplacement(t *testing.T) {
	if os.Getenv("MINIMALROUTER_NFT_FILTER_LAB") != "1" {
		t.Skip("requires disposable network namespace")
	}
	run := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command("nft", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("nft %v: %v %s", args, err, out)
		}
		return out
	}
	run("add", "table", "inet", "minimalrouter")
	defer exec.Command("nft", "delete", "table", "inet", "minimalrouter").Run()
	run("add", "set", "inet", "minimalrouter", "svc_youtube", "{ type ipv4_addr; flags timeout; timeout 4h; size 16384; }")
	run("add", "element", "inet", "minimalrouter", "svc_youtube", "{ 203.0.113.9 timeout 300s }")
	raw := run("-j", "list", "table", "inet", "minimalrouter")
	batch, err := serviceDestinationBatch(raw, map[string]bool{"svc_youtube": true}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replace.nft")
	text := "delete table inet minimalrouter\ntable inet minimalrouter {\n set svc_youtube { type ipv4_addr; flags timeout; timeout 4h; size 16384; }\n}\n" + string(batch)
	if err = os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	run("-f", path)
	raw = run("-j", "list", "set", "inet", "minimalrouter", "svc_youtube")
	if !bytes.Contains(raw, []byte("203.0.113.9")) {
		t.Fatal("destination lost across atomic replacement")
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}

// probeResponder answers the filter-generation TXT query after dropping a
// configured number of early packets, reproducing a resolver that is bound
// but not serving yet right after a restart with a large catalog.
func probeResponder(t *testing.T, drops int, revision uint64) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		seen := 0
		for {
			n, peer, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			seen++
			if seen <= drops {
				continue
			}
			var query dnsmessage.Message
			if query.Unpack(buffer[:n]) != nil || len(query.Questions) != 1 {
				continue
			}
			question := query.Questions[0]
			reply := dnsmessage.Message{
				Header:    dnsmessage.Header{ID: query.ID, Response: true, RCode: dnsmessage.RCodeSuccess},
				Questions: query.Questions,
				Answers: []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: 0},
					Body:   &dnsmessage.TXTResource{TXT: []string{strconv.FormatUint(revision, 10)}},
				}},
			}
			raw, err := reply.Pack()
			if err != nil {
				continue
			}
			_, _ = conn.WriteTo(raw, peer)
		}
	}()
	return conn.LocalAddr().String()
}

func TestDNSFilterProbeRetriesUntilServing(t *testing.T) {
	oldTimeout, oldAttempt := filterProbeTimeout, filterProbeAttemptTimeout
	filterProbeTimeout, filterProbeAttemptTimeout = 6*time.Second, 200*time.Millisecond
	t.Cleanup(func() { filterProbeTimeout, filterProbeAttemptTimeout = oldTimeout, oldAttempt })
	// The first five exchanges are lost as if the restarted resolver had not
	// started answering yet; a single-shot probe would fail this activation.
	address := probeResponder(t, 5, 7)
	start := time.Now()
	if err := filterResolverHealthyAt(7, address); err != nil {
		t.Fatalf("probe gave up on a slow-starting resolver: %v", err)
	}
	if elapsed := time.Since(start); elapsed > filterProbeTimeout {
		t.Fatalf("probe exceeded its budget: %v", elapsed)
	}
}

func TestDNSFilterProbeGivesUpBounded(t *testing.T) {
	oldTimeout := filterProbeTimeout
	filterProbeTimeout = 400 * time.Millisecond
	t.Cleanup(func() { filterProbeTimeout = oldTimeout })
	// Nothing answers here: the probe must fail, but within its budget rather
	// than hanging the activation or the status check.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := conn.LocalAddr().String()
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := filterResolverHealthyAt(7, address); err == nil {
		t.Fatal("silent resolver accepted")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("probe did not stay bounded: %v", elapsed)
	}
}
