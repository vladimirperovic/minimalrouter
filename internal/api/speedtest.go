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
	speedtestSuggestedPct  = 90
	speedtestHost          = "https://speed.cloudflare.com"
)

type speedtestResult struct {
	DownloadMbps          float64 `json:"download_mbps"`
	UploadMbps            float64 `json:"upload_mbps"`
	SuggestedDownloadMbps int     `json:"suggested_download_mbps"`
	SuggestedUploadMbps   int     `json:"suggested_upload_mbps"`
	QoSTemporarilyBypassed bool   `json:"qos_temporarily_bypassed"`
}

// speedtestMu serializes measurements. Two concurrent runs would saturate the
// same line and report half the real throughput to both callers.
var speedtestMu sync.Mutex

func (s *Server) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] POST %s from %s\n", r.URL.Path, r.RemoteAddr)

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

	// Bind the measurement itself to the request: if the operator closes the
	// tab or navigates away, the router stops pulling 75 MB through the WAN
	// link. WithQoSBypassed deliberately restores canonical QoS with its own
	// background timeout even after this request context is cancelled.
	ctx := r.Context()
	var dlMbps, ulMbps float64
	err := s.engine.WithQoSBypassed(ctx, func(measureCtx context.Context) error {
		var err error
		dlMbps, err = measureDownload(measureCtx, client)
		if err != nil {
			return fmt.Errorf("download speed test failed: %w", err)
		}
		ulMbps, err = measureUpload(measureCtx, client)
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
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func measureDownload(ctx context.Context, client *http.Client) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		speedtestHost+"/__down?bytes="+strconv.Itoa(speedtestDownloadBytes), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	start := time.Now()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, err
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
}

func (t *timedBody) Read(p []byte) (int, error) {
	if t.firstRead.IsZero() {
		t.firstRead = time.Now()
	}
	return t.inner.Read(p)
}

func measureUpload(ctx context.Context, client *http.Client) (float64, error) {
	// cf-based upload endpoint expects the bytes in the POST body; the
	// response body is empty, so speed is derived from how fast the body
	// drains into the connection.
	body := &timedBody{inner: randomReader(speedtestUploadBytes)}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		speedtestHost+"/__up?bytes="+strconv.Itoa(speedtestUploadBytes), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = speedtestUploadBytes

	fallbackStart := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	start := body.firstRead
	if start.IsZero() {
		start = fallbackStart
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}
	return mbps(speedtestUploadBytes, elapsed), nil
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
