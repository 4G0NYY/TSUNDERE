package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type httpsConfig struct {
	URL           string `json:"url"`
	Keyword       string `json:"keyword"`     // optional: body must contain this
	TimeoutSec    int    `json:"timeout_sec"` // default 10
	MaxStatus     int    `json:"max_status"`  // default 399: anything above is down
	SkipTLSVerify bool   `json:"skip_tls_verify"`
}

// HTTPS performs a GET and validates status code, optional keyword, and TLS
// certificate expiry (an expired cert counts as down even if the request
// technically succeeds with verification disabled).
func HTTPS(ctx context.Context, raw json.RawMessage) Result {
	var cfg httpsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || strings.TrimSpace(cfg.URL) == "" {
		return down("https monitor has no URL configured")
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 10
	}
	if cfg.MaxStatus <= 0 {
		cfg.MaxStatus = 399
	}

	client := &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
	if cfg.SkipTLSVerify {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig.InsecureSkipVerify = true
		client.Transport = tr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return down("bad URL: " + err.Error())
	}
	req.Header.Set("User-Agent", "TSUNDERE-agent/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return down(fmt.Sprintf("request failed: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode > cfg.MaxStatus {
		return down(fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		expiry := resp.TLS.PeerCertificates[0].NotAfter
		daysLeft := int(time.Until(expiry).Hours() / 24)
		if daysLeft < 0 {
			return down(fmt.Sprintf("TLS certificate expired on %s", expiry.Format("2006-01-02")))
		}
		if daysLeft <= 14 {
			msg += fmt.Sprintf(", cert expires in %dd!", daysLeft)
		}
	}

	if cfg.Keyword != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if !strings.Contains(string(body), cfg.Keyword) {
			return down(fmt.Sprintf("HTTP %d but keyword %q not found in body", resp.StatusCode, cfg.Keyword))
		}
	}
	return up(latency, msg)
}
