package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

type dnsConfig struct {
	Server     string `json:"server"`      // DNS server to test, e.g. "1.1.1.1" or "10.0.0.53:5353"
	Query      string `json:"query"`       // name to resolve, e.g. "example.com"
	RecordType string `json:"record_type"` // A | AAAA | CNAME | MX | NS | TXT
	Expected   string `json:"expected"`    // optional substring that must appear in an answer
	TimeoutSec int    `json:"timeout_sec"`
}

// DNS resolves cfg.Query against the *configured* DNS server (not the system
// resolver) — the point is testing whether that DNS server works.
func DNS(ctx context.Context, raw json.RawMessage) Result {
	var cfg dnsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Server == "" || cfg.Query == "" {
		return down("dns monitor needs both a server and a query name")
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 5
	}
	server := cfg.Server
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSec)*time.Second)
	defer cancel()

	start := time.Now()
	var answers []string
	var err error
	switch strings.ToUpper(cfg.RecordType) {
	case "", "A":
		var ips []net.IP
		ips, err = r.LookupIP(ctx, "ip4", cfg.Query)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "AAAA":
		var ips []net.IP
		ips, err = r.LookupIP(ctx, "ip6", cfg.Query)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "CNAME":
		var cname string
		cname, err = r.LookupCNAME(ctx, cfg.Query)
		if cname != "" {
			answers = append(answers, cname)
		}
	case "MX":
		var mxs []*net.MX
		mxs, err = r.LookupMX(ctx, cfg.Query)
		for _, mx := range mxs {
			answers = append(answers, mx.Host)
		}
	case "NS":
		var nss []*net.NS
		nss, err = r.LookupNS(ctx, cfg.Query)
		for _, ns := range nss {
			answers = append(answers, ns.Host)
		}
	case "TXT":
		answers, err = r.LookupTXT(ctx, cfg.Query)
	default:
		return down(fmt.Sprintf("unsupported record type %q", cfg.RecordType))
	}
	latency := time.Since(start)

	if err != nil {
		return down(fmt.Sprintf("query %s via %s failed: %v", cfg.Query, cfg.Server, err))
	}
	if len(answers) == 0 {
		return down(fmt.Sprintf("query %s via %s returned no answers", cfg.Query, cfg.Server))
	}
	if cfg.Expected != "" {
		found := false
		for _, a := range answers {
			if strings.Contains(a, cfg.Expected) {
				found = true
				break
			}
		}
		if !found {
			return down(fmt.Sprintf("answers %v do not contain %q", answers, cfg.Expected))
		}
	}
	return up(latency, strings.Join(answers, ", "))
}
