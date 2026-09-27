package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	speedtestDownloadBytes = 50 << 20
	speedtestUploadBytes   = 25 << 20
	estimateDownloadBytes  = 8 << 20
	estimateUploadBytes    = 4 << 20
	speedtestSuggestedPct  = 90

	// A phase measures for at most this much transfer time. Fast lines finish
	// on the byte limit first; a slow line yields a smaller but real sample
	// instead of timing out, which a fixed 50/25 MB sample under a 60 s client
	// timeout did on anything below roughly 7/3.3 Mbit/s.
	speedtestPhaseDuration = 15 * time.Second
	estimatePhaseDuration  = 8 * time.Second
	// minimumMeasuredBytes is the least traffic worth turning into a rate.
	minimumMeasuredBytes = 256 << 10
	// uploadProbeBytes calibrates the upload sample to the line. An upload
	// cannot be cut short and still be measured honestly: bytes handed to the
	// kernel are not bytes the server received, so the sample is sized to fit
	// the phase instead.
	uploadProbeBytes   = 256 << 10
	uploadProbeTimeout = 30 * time.Second
)

// speedtestHost is a variable only so tests can point the measurement at a
// local server; production always measures against Cloudflare.
var speedtestHost = "https://speed.cloudflare.com"

type speedtestResult struct {
	DownloadMbps           float64 `json:"download_mbps"`
	UploadMbps             float64 `json:"upload_mbps"`
	SuggestedDownloadMbps  int     `json:"suggested_download_mbps"`
	SuggestedUploadMbps    int     `json:"suggested_upload_mbps"`
	QoSTemporarilyBypassed bool    `json:"qos_temporarily_bypassed"`
	Estimate               bool    `json:"estimate"`
}

// speedtestMu serializes measurements. Two concurrent runs would saturate the
// same line and report half the real throughput to both callers.
var speedtestMu sync.Mutex

func (s *Server) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] POST %s from %s\n", r.URL.Path, r.RemoteAddr)

	mode := r.URL.Query().Get("mode")
	if mode != "" && mode != "estimate" {
		http.Error(w, "Unsupported speed test mode", http.StatusBadRequest)
		return
	}
	isEstimate := mode == "estimate"
	downloadBytes := int64(speedtestDownloadBytes)
	uploadBytes := int64(speedtestUploadBytes)
	phase := speedtestPhaseDuration
	if isEstimate {
		// First-run WAN estimation is advisory, not a benchmark. Keep it outside
		// the boot critical path and use a small sample so it does not consume a
		// full 75 MB measurement just to populate the overview hint.
		downloadBytes = estimateDownloadBytes
		uploadBytes = estimateUploadBytes
		phase = estimatePhaseDuration
	}

	if !speedtestMu.TryLock() {
		http.Error(w, "A speed test is already running.", http.StatusConflict)
		return
	}
	defer speedtestMu.Unlock()

	cfg := s.engine.GetCurrentConfig()
	qosWasEnabled := cfg.QoS.Enabled

	client := newSpeedtestClient()
	// This transport is created per measurement; without this its keep-alive
	// connections would linger in an appliance with a 128 MiB budget.
	defer client.CloseIdleConnections()

	// Bind the measurement itself to the request: if the operator closes the
	// tab or navigates away, the router stops pulling test traffic through the
	// WAN link. WithQoSBypassed deliberately restores canonical QoS with its own
	// background timeout even after this request context is cancelled.
	ctx := r.Context()
	var dlMbps, ulMbps float64
	err := s.engine.WithQoSBypassed(ctx, func(measureCtx context.Context) error {
		var err error
		dlMbps, err = measureDownload(measureCtx, client, downloadBytes, phase)
		if err != nil {
			return fmt.Errorf("download speed test failed: %w", err)
		}
		ulMbps, err = measureUpload(measureCtx, client, uploadBytes, phase)
		if err != nil {
			return fmt.Errorf("upload speed test failed: %w", err)
		}
		return nil
	})
	if err != nil {
		http.Error(w, "Speed test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	res := speedtestResult{
		DownloadMbps:           dlMbps,
		UploadMbps:             ulMbps,
		SuggestedDownloadMbps:  roundPct(dlMbps, speedtestSuggestedPct),
		SuggestedUploadMbps:    roundPct(ulMbps, speedtestSuggestedPct),
		QoSTemporarilyBypassed: qosWasEnabled,
		Estimate:               isEstimate,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// newSpeedtestClient bounds connection setup and the wait for response
// headers, but not the transfer itself: each phase enforces its own transfer
// budget, so an overall client timeout would only turn a slow line into a
// failure.
func newSpeedtestClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			// The appliance has IPv6 disabled (no default route), so force IPv4
			// to avoid a silent dial timeout when the resolver returns v6 first.
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
				return d.DialContext(ctx, "tcp4", addr)
			},
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// minimumSampleFraction is how much of the requested payload must actually move
// before a measurement is worth reporting. A short answer — an error page, a
// truncated transfer — divided by a near-zero duration produces an arbitrarily
// large "speed" that would then be offered as a QoS bandwidth suggestion.
const minimumSampleFraction = 0.5

// measureDownload reads up to sampleBytes, stopping after budget of transfer
// time. The rate is derived from bytes actually received.
func measureDownload(ctx context.Context, client *http.Client, sampleBytes int64, budget time.Duration) (float64, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet,
		speedtestHost+"/__down?bytes="+strconv.FormatInt(sampleBytes, 10), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("download endpoint returned HTTP %d", resp.StatusCode)
	}

	var budgetReached atomic.Bool
	timer := time.AfterFunc(budget, func() {
		budgetReached.Store(true)
		cancel()
	})
	start := time.Now()
	// Bound the read: the measurement asked for sampleBytes and must not be
	// turned into an unbounded transfer by a misbehaving or redirected host.
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, sampleBytes))
	elapsed := time.Since(start)
	timer.Stop()
	stoppedByBudget := budgetReached.Load()
	if err != nil && !stoppedByBudget {
		return 0, err
	}
	if !stoppedByBudget && n < int64(float64(sampleBytes)*minimumSampleFraction) {
		return 0, fmt.Errorf("download sample was too small to measure (%d of %d bytes)", n, sampleBytes)
	}
	if n < minimumMeasuredBytes {
		return 0, fmt.Errorf("download moved too little data to measure (%d bytes in %s)", n, elapsed.Round(time.Second))
	}
	return mbps(n, positiveSeconds(elapsed)), nil
}

// timedBody starts the clock at the first byte the transport actually reads
// from the request body. Timing from before client.Do() charged DNS, the TCP
// handshake and the TLS handshake to the upload, which understated every result
// on a high-latency line. Its counters are atomic because the transport may
// still be reading the body when a server answers early.
type timedBody struct {
	inner io.Reader
	// created carries a monotonic clock reading. Offsets from it, not wall
	// time, time the transfer: first-run estimates run while NTP may still
	// step the clock.
	created time.Time
	// firstRead is the offset of the first Read call from created, plus one
	// nanosecond so that zero still means "never read".
	firstRead atomic.Int64
	// sent counts the bytes the transport actually pulled from this body. The
	// upload rate must be derived from these, never from the planned size: a
	// server that answers before reading the body would otherwise divide the
	// full sample by a near-zero duration.
	sent atomic.Int64
}

func (t *timedBody) Read(p []byte) (int, error) {
	t.firstRead.CompareAndSwap(0, int64(time.Since(t.created))+1)
	n, err := t.inner.Read(p)
	t.sent.Add(int64(n))
	return n, err
}

// measureUpload calibrates with a small probe, then uploads a sample sized to
// take about budget on this line (at most maxBytes).
func measureUpload(ctx context.Context, client *http.Client, maxBytes int64, budget time.Duration) (float64, error) {
	probeBytes := min(int64(uploadProbeBytes), maxBytes)
	probeRate, err := uploadOnce(ctx, client, probeBytes, uploadProbeTimeout)
	if err != nil {
		return 0, err
	}
	sampleBytes := min(max(int64(probeRate*budget.Seconds()), int64(minimumMeasuredBytes)), maxBytes)
	if sampleBytes <= probeBytes {
		// The line is so slow that the probe already filled the phase.
		return probeRate * 8 / 1e6, nil
	}
	rate, err := uploadOnce(ctx, client, sampleBytes, 2*budget+15*time.Second)
	if err != nil {
		return 0, err
	}
	return rate * 8 / 1e6, nil
}

// uploadOnce sends sampleBytes and returns the received rate in bytes/second.
// Cloudflare's upload endpoint answers after it has consumed the body, so the
// span from the first body read to the response is the transfer time.
func uploadOnce(ctx context.Context, client *http.Client, sampleBytes int64, timeout time.Duration) (float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body := &timedBody{inner: randomReader(sampleBytes), created: time.Now()}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost,
		speedtestHost+"/__up?bytes="+strconv.FormatInt(sampleBytes, 10), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = sampleBytes

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	answered := time.Now()
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("upload endpoint returned HTTP %d", resp.StatusCode)
	}

	firstRead := body.firstRead.Load()
	sent := body.sent.Load()
	if firstRead == 0 {
		return 0, fmt.Errorf("upload endpoint answered without reading the request body; no upload rate was measured")
	}
	if sent < int64(float64(sampleBytes)*minimumSampleFraction) {
		return 0, fmt.Errorf("upload sample was too small to measure (%d of %d bytes)", sent, sampleBytes)
	}
	elapsed := answered.Sub(body.created) - time.Duration(firstRead-1)
	return float64(sent) / positiveSeconds(elapsed), nil
}

// positiveSeconds guards a rate against a zero or negative duration from a
// coarse or adjusted clock.
func positiveSeconds(d time.Duration) float64 {
	if d <= 0 {
		return time.Millisecond.Seconds()
	}
	return d.Seconds()
}

func mbps(bytes int64, seconds float64) float64 {
	return float64(bytes) * 8 / 1e6 / seconds
}

func roundPct(mbps float64, pct int) int {
	v := int(mbps * float64(pct) / 100.0)
	if v < 1 {
		return 1
	}
	return v
}

// randomReader yields deterministic-length random bytes so the upload test
// carries real payload instead of compressible zeros.
func randomReader(n int64) io.Reader {
	return io.LimitReader(rand.Reader, n)
}
