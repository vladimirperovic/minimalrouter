package api

import (
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTOTPReplayCanonicalAliasesAreConsumedOnceConcurrently(t *testing.T) {
	s := &Server{totpReplay: make(map[[sha256.Size]byte]time.Time)}
	secrets := []string{"JBSWY3DPEHPK3PXP", "jbswy3dpehpk3pxp", "JBSW Y3DP EHPK 3PXP", "JBSWY3DP\nEHPK3PXP"}
	codes := []string{"123456", " 123456 ", "\t123456\n", "\u2003123456\u2003"}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if s.consumeTOTP(secrets[i%len(secrets)], codes[i%len(codes)]) {
				accepted.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("canonical OTP consumed %d times", accepted.Load())
	}
	if !s.consumeTOTP(secrets[0], "654321") {
		t.Fatal("a distinct code was incorrectly marked as replay")
	}
}
