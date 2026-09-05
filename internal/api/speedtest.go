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
	"time"
)

const (
	speedtestDownloadBytes = 50 << 20
	speedtestUploadBytes   = 25 << 20
	estimateDownloadBytes  = 8 << 20
	estimateUploadBytes    = 4 << 20
	speedtestSuggestedPct  = 90
	speedtestHost          = "https://speed.cloudflare.com"
)

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
	if isEstimate {
		// First-run WAN estimation is advisory, not a benchmark. Keep it outside
		// the boot critical path and use a small sample so it does not consume a
		// full 75 MB measurement just to populate the overview hint.
		downloadBytes = estimateDownloadBytes
		uploadBytes = estimateUploadBytes
	}

	if !speedtestMu.TryLock() {
		http.Error(w, "A speed test is already running.", http.StatusConflict)
		return
	}
	defer speedtestMu.Unlock()

	cfg := s.engine.GetCurrentConfig()
	qosWasEnabled := cfg.QoS.Enabled

	// The appliance has IPv6 disabled (no default route), so force IPv4 to
	// avoid a silent dial timeout when the resolver returns v6 addresses first.
	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
				return d.DialContext(ctx, "tcp4", addr)
			},
		},
	}
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
		dlMbps, err = measureDownload(measureCtx, client, downloadBytes)
		if err != nil {
			return fmt.Errorf("download speed test failed: %w", err)
		}
		ulMbps, err = measureUpload(measureCtx, client, uploadBytes)
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

// minimumSampleFraction is how much of the requested payload must actually move
// before a measurement is worth reporting. A short answer — an error page, a
// truncated transfer — divided by a near-zero duration produces an arbitrarily
// large "speed" that would then be offered as a QoS bandwidth suggestion.
const minimumSampleFraction = 0.5

func measureDownload(ctx context.Context, client *http.Client, sampleBytes int64) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
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

	start := time.Now()
	// Bound the read: the measurement asked for sampleBytes and must not be
	// turned into an unbounded transfer by a misbehaving or redirected host.
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, sampleBytes))
	if err != nil {
		return 0, err
	}
	if n < int64(float64(sampleBytes)*minimumSampleFraction) {
		return 0, fmt.Errorf("download sample was too small to measure (%d of %d bytes)", n, sampleBytes)
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}
	return mbps(n, elapsed), nil
}

// timedBody starts the clock at the first byte the transport actually reads
// from the request body. Timing from before client.Do() charged DNS, the TCP
// handshake and the TLS handshake to the upload, which understated every result
// on a high-latency line.
type timedBody struct {
	inner     io.Reader
	firstRead time.Time
	// sent counts the bytes the transport actually pulled from this body. The
	// upload rate must be derived from these, never from the planned size: a
	// server that answers before reading the body would otherwise divide the
	// full sample by a near-zero duration.
	sent int64
}

func (t *timedBody) Read(p []byte) (int, error) {
	if t.firstRead.IsZero() {
		t.firstRead = time.Now()
	}
	n, err := t.inner.Read(p)
	t.sent += int64(n)
	return n, err
}

func measureUpload(ctx context.Context, client *http.Client, sampleBytes int64) (float64, error) {
	// Cloudflare's upload endpoint expects the bytes in the POST body; the
	// response body is empty, so speed is derived from how fast the body drains
	// into the connection.
	body := &timedBody{inner: randomReader(sampleBytes)}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
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
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("upload endpoint returned HTTP %d", resp.StatusCode)
	}

	if body.firstRead.IsZero() {
		return 0, fmt.Errorf("upload endpoint answered without reading the request body; no upload rate was measured")
	}
	if body.sent < int64(float64(sampleBytes)*minimumSampleFraction) {
		return 0, fmt.Errorf("upload sample was too small to measure (%d of %d bytes)", body.sent, sampleBytes)
	}
	elapsed := time.Since(body.firstRead).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}
	return mbps(body.sent, elapsed), nil
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
