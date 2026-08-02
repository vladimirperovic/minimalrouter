package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
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
}

func (s *Server) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] POST %s from %s\n", r.URL.Path, r.RemoteAddr)

	cfg := s.engine.GetCurrentConfig()
	if cfg.QoS.Enabled {
		http.Error(w, "Disable QoS before running a speed test: an active shaper would report its own limit, not your line speed.", http.StatusConflict)
		return
	}

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

	dlMbps, err := measureDownload(client)
	if err != nil {
		http.Error(w, "Download speed test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ulMbps, err := measureUpload(client)
	if err != nil {
		http.Error(w, "Upload speed test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	res := speedtestResult{
		DownloadMbps:          dlMbps,
		UploadMbps:            ulMbps,
		SuggestedDownloadMbps: roundPct(dlMbps, speedtestSuggestedPct),
		SuggestedUploadMbps:   roundPct(ulMbps, speedtestSuggestedPct),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func measureDownload(client *http.Client) (float64, error) {
	resp, err := client.Get(speedtestHost + "/__down?bytes=" + strconv.Itoa(speedtestDownloadBytes))
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

func measureUpload(client *http.Client) (float64, error) {
	// cf-based upload endpoint expects the bytes in the POST body; the
	// response body is empty, so speed is derived from how fast the body
	// drains into the connection.
	req, err := http.NewRequest(http.MethodPost, speedtestHost+"/__up?bytes="+strconv.Itoa(speedtestUploadBytes), randomReader(speedtestUploadBytes))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = speedtestUploadBytes

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

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
