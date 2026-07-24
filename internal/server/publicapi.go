package server

// Read-only public API (v1) for external dashboards such as the TSUNDERE
// Portal. Everything here is GET-only: there is deliberately no path that
// mutates state. Access is gated by API keys minted in the admin UI.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// withCORS lets the Cloudflare-hosted dashboard (a different origin) call the
// read-only API straight from the browser, and answers CORS preflight. It also
// enforces GET-only — a hard guarantee that this surface never mutates.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, X-API-Key, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			next(w, r)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "this API is read-only — only GET is allowed")
		}
	}
}

func apiKeyFromRequest(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(b)
	}
	return ""
}

// requireAPIKey guards the read-only API. In dev-no-auth mode any request is
// allowed, mirroring the admin bypass, so the dashboard can be developed
// against a local server without minting a key first.
func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return withCORS(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.DevNoAuth {
			key := apiKeyFromRequest(r)
			if key == "" {
				writeErr(w, http.StatusUnauthorized, "missing API key — send it as 'X-API-Key' or 'Authorization: Bearer <key>'")
				return
			}
			if _, err := s.store.ValidateAPIKey(key); err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid API key. Hmph, nice try.")
				return
			}
		}
		next(w, r)
	})
}

// ---------- /api/v1 read-only endpoints ----------

// GET /api/v1/status — a compact health summary across all monitors and agents.
func (s *Server) handleV1Status(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.store.ListMonitors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	agents, err := s.store.ListAgents()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var up, down, pending int
	for _, m := range monitors {
		switch m.LastStatus {
		case StatusUp:
			up++
		case StatusDown:
			down++
		default:
			pending++
		}
	}
	online := 0
	for _, a := range agents {
		if a.Online {
			online++
		}
	}
	overall := "up"
	if down > 0 {
		overall = "down"
	} else if up == 0 {
		overall = "pending"
	}
	writeJSON(w, map[string]any{
		"overall":          overall,
		"monitors_total":   len(monitors),
		"monitors_up":      up,
		"monitors_down":    down,
		"monitors_pending": pending,
		"agents_total":     len(agents),
		"agents_online":    online,
		"generated_at":     time.Now().Unix(),
	})
}

// nodeView is the sanitised, agent-token-free shape returned for /api/v1/nodes.
type nodeView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	Online     bool   `json:"online"`
	LastSeen   int64  `json:"last_seen"`
	Containers int    `json:"containers"`
	Services   int    `json:"services"`
	CreatedAt  int64  `json:"created_at"`
}

// GET /api/v1/nodes — the monitored hosts (agents), without any secrets.
func (s *Server) handleV1Nodes(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]nodeView, 0, len(agents))
	for _, a := range agents {
		var inv DockerInventory
		_ = json.Unmarshal(a.Inventory, &inv)
		out = append(out, nodeView{
			ID: a.ID, Name: a.Name, Hostname: a.Hostname, Online: a.Online, LastSeen: a.LastSeen,
			Containers: len(inv.Containers), Services: len(inv.Services), CreatedAt: a.CreatedAt,
		})
	}
	writeJSON(w, out)
}

// GET /api/v1/monitors — every monitor with its current status and target.
func (s *Server) handleV1Monitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.store.ListMonitors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, monitors)
}

// metricView is one monitor's live telemetry snapshot.
type metricView struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	AgentName     string  `json:"agent_name"`
	AgentHostname string  `json:"agent_hostname"`
	Status        int     `json:"status"`
	Enabled       bool    `json:"enabled"`
	LatencyMS     float64 `json:"latency_ms"`
	Message       string  `json:"message"`
	CheckedAt     int64   `json:"checked_at"`
	Uptime24h     any     `json:"uptime_24h"`
	Uptime30d     any     `json:"uptime_30d"`
}

// GET /api/v1/metrics — per-monitor latency + uptime, for gauges and graphs.
func (s *Server) handleV1Metrics(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.store.ListMonitors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	nowT := time.Now()
	since24 := nowT.Add(-24 * time.Hour).Unix()
	since30 := nowT.Add(-30 * 24 * time.Hour).Unix()
	out := make([]metricView, 0, len(monitors))
	for _, m := range monitors {
		mv := metricView{
			ID: m.ID, Name: m.Name, Type: m.Type, AgentName: m.AgentName,
			AgentHostname: m.AgentHostname, Status: m.LastStatus, Enabled: m.Enabled,
		}
		if hb, err := s.store.LastHeartbeat(m.ID); err == nil {
			mv.LatencyMS = hb.LatencyMS
			mv.Message = hb.Message
			mv.CheckedAt = hb.CheckedAt
		}
		u24, ok24, _ := s.store.Uptime(m.ID, since24)
		u30, ok30, _ := s.store.Uptime(m.ID, since30)
		mv.Uptime24h = uptimeOrNil(u24, ok24)
		mv.Uptime30d = uptimeOrNil(u30, ok30)
		out = append(out, mv)
	}
	writeJSON(w, out)
}

// GET /api/v1/monitors/{id}/heartbeats?hours=N — a raw time-series for one
// monitor, for sparklines and latency charts.
func (s *Server) handleV1Heartbeats(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, err := s.store.GetMonitor(id); err != nil {
		writeErr(w, http.StatusNotFound, "no such monitor")
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24*90 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	beats, err := s.store.HeartbeatsSince(id, since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u24, ok24, _ := s.store.Uptime(id, time.Now().Add(-24*time.Hour).Unix())
	u30, ok30, _ := s.store.Uptime(id, time.Now().Add(-30*24*time.Hour).Unix())
	writeJSON(w, map[string]any{
		"monitor_id": id,
		"heartbeats": beats,
		"uptime_24h": uptimeOrNil(u24, ok24),
		"uptime_30d": uptimeOrNil(u30, ok30),
	})
}

// GET /api/v1/logs?severity=up|down|pending&limit=N — recent events across all
// monitors, newest first.
func (s *Server) handleV1Logs(w http.ResponseWriter, r *http.Request) {
	status := -1
	switch strings.ToLower(r.URL.Query().Get("severity")) {
	case "down", "critical", "error":
		status = StatusDown
	case "up", "ok", "info":
		status = StatusUp
	case "pending":
		status = StatusPending
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.store.RecentEvents(limit, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, events)
}

// ---------- admin: API-key management ----------

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "a name is required")
		return
	}
	key, plaintext, err := s.store.CreateAPIKey(body.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The plaintext key is shown exactly once; only its hash is kept.
	writeJSON(w, map[string]any{"api_key": key, "key": plaintext})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteAPIKey(id); err != nil {
		writeErr(w, http.StatusNotFound, "API key not found")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
