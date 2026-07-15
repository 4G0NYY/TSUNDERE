// Package agent implements the TSUNDERE agent: it pulls its monitor list from
// the server, runs the checks locally, and pushes results back. Pull-based, so
// agents need no inbound ports — only outbound HTTPS to the server.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/4G0NYY/tsundere/internal/agent/checks"
)

type Config struct {
	ServerURL  string // e.g. https://status.example.com
	Token      string // agent token from the admin UI
	DockerHost string // unix socket path or tcp:// URL; empty disables docker checks
}

type monitorDef struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	IntervalSec int             `json:"interval_sec"`
	Config      json.RawMessage `json:"config"`
}

type checkResult struct {
	MonitorID int64   `json:"monitor_id"`
	Status    int     `json:"status"`
	LatencyMS float64 `json:"latency_ms"`
	Message   string  `json:"message"`
	CheckedAt int64   `json:"checked_at"`
}

type Agent struct {
	cfg      Config
	client   *http.Client
	docker   *checks.DockerClient
	hostname string // OS hostname, reported to the server on every request

	mu      sync.Mutex
	runners map[int64]*runner
}

type runner struct {
	def    monitorDef
	cancel context.CancelFunc
}

func New(cfg Config) *Agent {
	hostname, _ := os.Hostname()
	a := &Agent{
		cfg:      cfg,
		client:   &http.Client{Timeout: 30 * time.Second},
		hostname: hostname,
		runners:  map[int64]*runner{},
	}
	if cfg.DockerHost != "" {
		a.docker = checks.NewDockerClient(cfg.DockerHost)
	}
	return a
}

func (a *Agent) Run(ctx context.Context) error {
	log.Printf("TSUNDERE agent starting, server=%s", a.cfg.ServerURL)
	go a.inventoryLoop(ctx)

	// Config poll loop: fetch assigned monitors, reconcile runners.
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if err := a.syncConfig(ctx); err != nil {
			log.Printf("config sync failed: %v (will retry)", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (a *Agent) syncConfig(ctx context.Context) error {
	var resp struct {
		AgentName string       `json:"agent_name"`
		Monitors  []monitorDef `json:"monitors"`
	}
	if err := a.apiGet(ctx, "/api/agent/config", &resp); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	wanted := map[int64]monitorDef{}
	for _, def := range resp.Monitors {
		wanted[def.ID] = def
	}

	// Stop runners for removed/changed monitors.
	for id, r := range a.runners {
		def, ok := wanted[id]
		if !ok || !sameDef(r.def, def) {
			r.cancel()
			delete(a.runners, id)
		}
	}
	// Start runners for new monitors.
	for id, def := range wanted {
		if _, ok := a.runners[id]; ok {
			continue
		}
		rctx, cancel := context.WithCancel(ctx)
		a.runners[id] = &runner{def: def, cancel: cancel}
		go a.runMonitor(rctx, def)
		log.Printf("monitor %d (%s) scheduled every %ds", def.ID, def.Type, def.IntervalSec)
	}
	return nil
}

func sameDef(a, b monitorDef) bool {
	return a.Type == b.Type && a.IntervalSec == b.IntervalSec && bytes.Equal(a.Config, b.Config)
}

func (a *Agent) runMonitor(ctx context.Context, def monitorDef) {
	interval := time.Duration(def.IntervalSec) * time.Second
	if interval < 10*time.Second {
		interval = time.Minute
	}
	// First check runs immediately, then on the interval.
	for {
		a.executeAndReport(ctx, def)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (a *Agent) executeAndReport(ctx context.Context, def monitorDef) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var res checks.Result
	switch def.Type {
	case "ping":
		res = checks.Ping(cctx, def.Config)
	case "dns":
		res = checks.DNS(cctx, def.Config)
	case "https":
		res = checks.HTTPS(cctx, def.Config)
	case "docker":
		if a.docker == nil {
			res = checks.Result{Up: false, Message: "docker checks disabled on this agent (no docker host configured)"}
		} else {
			res = checks.Docker(cctx, a.docker, def.Config)
		}
	default:
		res = checks.Result{Up: false, Message: "agent does not understand monitor type " + def.Type}
	}

	status := 0
	if res.Up {
		status = 1
	}
	out := checkResult{
		MonitorID: def.ID,
		Status:    status,
		LatencyMS: float64(res.Latency.Milliseconds()),
		Message:   res.Message,
		CheckedAt: time.Now().Unix(),
	}
	if err := a.apiPost(ctx, "/api/agent/results", map[string]any{"results": []checkResult{out}}, nil); err != nil {
		log.Printf("reporting result for monitor %d failed: %v", def.ID, err)
	}
}

// inventoryLoop reports docker containers/services so the admin UI can offer
// a mapping dropdown.
func (a *Agent) inventoryLoop(ctx context.Context) {
	if a.docker == nil {
		return
	}
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		inv, err := a.docker.Inventory(ctx)
		if err != nil {
			log.Printf("docker inventory failed: %v", err)
		} else if err := a.apiPost(ctx, "/api/agent/inventory", inv, nil); err != nil {
			log.Printf("posting inventory failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- tiny API client ----

func (a *Agent) apiGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.ServerURL+path, nil)
	if err != nil {
		return err
	}
	return a.do(req, out)
}

func (a *Agent) apiPost(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ServerURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return a.do(req, out)
}

func (a *Agent) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	if a.hostname != "" {
		req.Header.Set("X-Tsundere-Hostname", a.hostname)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
