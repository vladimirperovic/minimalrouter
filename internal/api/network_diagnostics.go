package api

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

type diagnosticCheck struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type networkDiagnosticResponse struct {
	Overall string                     `json:"overall"`
	Cause   string                     `json:"cause"`
	Checks  map[string]diagnosticCheck `json:"checks"`
}

func (s *Server) handleNetworkDiagnose(w http.ResponseWriter, r *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		writeGatewayJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Gateway monitoring is unavailable."})
		return
	}
	summary := monitor.Summary()
	checks := map[string]diagnosticCheck{}
	checks["pppoe"] = diagnosticCheck{OK: summary.Link.Connected, Detail: "PPPoE link is disconnected"}
	if summary.Link.Connected {
		checks["pppoe"] = diagnosticCheck{OK: true, Detail: "PPPoE link is connected"}
	}

	reachable := 0
	for _, target := range summary.Targets {
		if target.Reachable {
			reachable++
		}
	}
	checks["internet"] = diagnosticCheck{OK: reachable > 0, Detail: "No monitoring target replied"}
	if reachable > 0 {
		checks["internet"] = diagnosticCheck{OK: true, Detail: "Public monitoring target reachable"}
	}

	dnsCtx, dnsCancel := context.WithTimeout(r.Context(), 3*time.Second)
	_, dnsErr := net.DefaultResolver.LookupHost(dnsCtx, "example.com")
	dnsCancel()
	checks["dns"] = diagnosticCheck{OK: dnsErr == nil, Detail: "DNS lookup failed"}
	if dnsErr == nil {
		checks["dns"] = diagnosticCheck{OK: true, Detail: "DNS resolution works"}
	}

	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	httpsReq, _ := http.NewRequestWithContext(r.Context(), http.MethodHead, "https://example.com/", nil)
	httpsResp, httpsErr := client.Do(httpsReq)
	if httpsResp != nil {
		_ = httpsResp.Body.Close()
	}
	httpsOK := httpsErr == nil && httpsResp != nil && httpsResp.StatusCode >= 200 && httpsResp.StatusCode < 500
	checks["https"] = diagnosticCheck{OK: httpsOK, Detail: "HTTPS request failed"}
	if httpsOK {
		checks["https"] = diagnosticCheck{OK: true, Detail: "HTTPS connectivity works"}
	}

	cause := "healthy"
	overall := "healthy"
	switch {
	case !checks["pppoe"].OK:
		cause, overall = "pppoe_link", "failed"
	case !checks["internet"].OK:
		cause, overall = "internet_reachability", "failed"
	case !checks["dns"].OK:
		cause, overall = "dns", "degraded"
	case !checks["https"].OK:
		cause, overall = "https", "degraded"
	}
	writeGatewayJSON(w, http.StatusOK, networkDiagnosticResponse{Overall: overall, Cause: cause, Checks: checks})
}
