package release

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestChannelReselectsDuringCooldownAndAfter304(t *testing.T) {
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"catalog"`)
		fmt.Fprintf(w, "[%s,%s]", releaseJSON("v1.2.0-beta.1", true, amd64Assets()...), releaseJSON("v1.1.0", false, amd64Assets()...))
	})
	channel := ChannelBeta
	checker.channelFunc = func() Channel { return channel }
	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checker.Snapshot().Candidate.Version != "1.2.0-beta.1" {
		t.Fatal("beta candidate missing")
	}
	channel = ChannelStable
	for attempt := 0; attempt < 2; attempt++ {
		s := checker.Snapshot()
		if s.Channel != ChannelStable || s.Candidate == nil || s.Candidate.Prerelease || s.Candidate.Version != "1.1.0" {
			t.Fatalf("stale channel candidate: %+v", s)
		}
		clock.Advance(DefaultCooldown + time.Second)
		if _, err := checker.CheckNow(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	channel = ChannelBeta
	if checker.Snapshot().Candidate.Version != "1.2.0-beta.1" {
		t.Fatal("cached catalog lost beta on channel change")
	}
}
