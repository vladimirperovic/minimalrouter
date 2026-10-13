package main

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// serveFakeDNS answers every A query with 192.0.2.10 and every other type with
// an empty NOERROR response, until the returned stop function is called. Before
// readyAt it answers REFUSED instead, like a resolver that is still loading.
func serveFakeDNS(t *testing.T, conn net.PacketConn, readyAt time.Time) (stop func()) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var parser dnsmessage.Parser
			header, err := parser.Start(buf[:n])
			if err != nil {
				continue
			}
			question, err := parser.Question()
			if err != nil {
				continue
			}
			ready := !time.Now().Before(readyAt)
			reply := dnsmessage.Header{ID: header.ID, Response: true, RecursionAvailable: true}
			if !ready {
				reply.RCode = dnsmessage.RCodeRefused
			}
			builder := dnsmessage.NewBuilder(nil, reply)
			builder.EnableCompression()
			_ = builder.StartQuestions()
			_ = builder.Question(question)
			_ = builder.StartAnswers()
			if ready && question.Type == dnsmessage.TypeA {
				_ = builder.AResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60},
					dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}})
			}
			message, err := builder.Finish()
			if err == nil {
				_, _ = conn.WriteTo(message, from)
			}
		}
	}()
	return func() { _ = conn.Close(); wg.Wait() }
}

func listenFakeDNS(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// Regression for the live v0.3.0 finding: with ~510k DNS-filter domains loaded
// dnsmasq refused queries for a moment after restart, and two immediate lookups
// rolled back every WAN/upstream-DNS/device-profile change.
//
// The resolver keeps its port from the start and refuses until it is ready.
// Freeing a port and binding it again later raced other packages' sockets under
// `go test ./...`: the fake resolver then never started and the test failed.
func TestFunctionalDNSWaitsForSlowResolverStart(t *testing.T) {
	conn := listenFakeDNS(t)
	stop := serveFakeDNS(t, conn, time.Now().Add(700*time.Millisecond))
	defer stop()

	begin := time.Now()
	if err := verifyFunctionalDNSAt(conn.LocalAddr().String(), 5*time.Second, 100*time.Millisecond); err != nil {
		t.Fatalf("resolver that becomes ready after 700ms must pass verification: %v", err)
	}
	if elapsed := time.Since(begin); elapsed < 600*time.Millisecond {
		t.Fatalf("verification passed while the resolver was still refusing (%s)", elapsed)
	}
}

func TestFunctionalDNSFailsAfterReadinessDeadline(t *testing.T) {
	conn := listenFakeDNS(t)
	stop := serveFakeDNS(t, conn, time.Now().Add(time.Hour))
	defer stop()

	begin := time.Now()
	err := verifyFunctionalDNSAt(conn.LocalAddr().String(), 600*time.Millisecond, 100*time.Millisecond)
	if err == nil {
		t.Fatal("a resolver that keeps refusing must fail verification")
	}
	if !strings.Contains(err.Error(), "within 600ms") {
		t.Fatalf("error should name the readiness bound: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("verification overran its readiness bound: %s", elapsed)
	}
}

func TestFunctionalDNSPassesImmediatelyWhenResolverAnswers(t *testing.T) {
	conn := listenFakeDNS(t)
	stop := serveFakeDNS(t, conn, time.Time{})
	defer stop()
	begin := time.Now()
	if err := verifyFunctionalDNSAt(conn.LocalAddr().String(), 5*time.Second, time.Second); err != nil {
		t.Fatalf("answering resolver must pass: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 900*time.Millisecond {
		t.Fatalf("healthy resolver should not wait for a retry interval (%s)", elapsed)
	}
}
