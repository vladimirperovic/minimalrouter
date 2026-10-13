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
// an empty NOERROR response, until the returned stop function is called.
func serveFakeDNS(t *testing.T, conn net.PacketConn) (stop func()) {
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
			builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, RecursionAvailable: true})
			builder.EnableCompression()
			_ = builder.StartQuestions()
			_ = builder.Question(question)
			_ = builder.StartAnswers()
			if question.Type == dnsmessage.TypeA {
				_ = builder.AResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60},
					dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}})
			}
			reply, err := builder.Finish()
			if err == nil {
				_, _ = conn.WriteTo(reply, from)
			}
		}
	}()
	return func() { _ = conn.Close(); wg.Wait() }
}

func freeUDPAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := conn.LocalAddr().String()
	_ = conn.Close()
	return address
}

// Regression for the live v0.3.0 finding: with ~510k DNS-filter domains loaded
// dnsmasq refused queries for a moment after restart, and two immediate lookups
// rolled back every WAN/upstream-DNS/device-profile change.
func TestFunctionalDNSWaitsForSlowResolverStart(t *testing.T) {
	address := freeUDPAddress(t)
	started := make(chan func(), 1)
	go func() {
		time.Sleep(700 * time.Millisecond)
		conn, err := net.ListenPacket("udp", address)
		if err != nil {
			started <- nil
			return
		}
		started <- serveFakeDNS(t, conn)
	}()
	defer func() {
		if stop := <-started; stop != nil {
			stop()
		}
	}()

	begin := time.Now()
	if err := verifyFunctionalDNSAt(address, 5*time.Second, 100*time.Millisecond); err != nil {
		t.Fatalf("resolver that starts after 700ms must pass verification: %v", err)
	}
	if elapsed := time.Since(begin); elapsed < 600*time.Millisecond {
		t.Fatalf("verification passed before the resolver existed (%s)", elapsed)
	}
}

func TestFunctionalDNSFailsAfterReadinessDeadline(t *testing.T) {
	address := freeUDPAddress(t)
	begin := time.Now()
	err := verifyFunctionalDNSAt(address, 600*time.Millisecond, 100*time.Millisecond)
	if err == nil {
		t.Fatal("a resolver that never answers must fail verification")
	}
	if !strings.Contains(err.Error(), "within 600ms") {
		t.Fatalf("error should name the readiness bound: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("verification overran its readiness bound: %s", elapsed)
	}
}

func TestFunctionalDNSPassesImmediatelyWhenResolverAnswers(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := serveFakeDNS(t, conn)
	defer stop()
	begin := time.Now()
	if err := verifyFunctionalDNSAt(conn.LocalAddr().String(), 5*time.Second, time.Second); err != nil {
		t.Fatalf("answering resolver must pass: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 900*time.Millisecond {
		t.Fatalf("healthy resolver should not wait for a retry interval (%s)", elapsed)
	}
}
