package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return false
	}
	return true
}

// ---------- me / overview ----------

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == "" {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	writeJSON(w, map[string]any{"login": user, "dev_no_auth": s.cfg.DevNoAuth})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
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
	agentsOnline := 0
	for _, a := range agents {
		if a.Online {
			agentsOnline++
		}
	}
	writeJSON(w, map[string]any{
		"monitors_up":      up,
		"monitors_down":    down,
		"monitors_pending": pending,
		"agents_total":     len(agents),
		"agents_online":    agentsOnline,
	})
}

// ---------- agents ----------

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "agent name is required")
		return
	}
	agent, token, err := s.store.CreateAgent(body.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The plaintext token is shown exactly once; only its hash is stored.
	writeJSON(w, map[string]any{"agent": agent, "token": token})
}

func (s *Server) handleRenameAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "agent name is required")
		return
	}
	if err := s.store.RenameAgent(id, strings.TrimSpace(body.Name)); err != nil {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRegenerateAgentToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	token, err := s.store.RegenerateAgentToken(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, map[string]any{"token": token})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteAgent(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- monitors ----------

func validateMonitor(m *Monitor) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return fmt.Errorf("monitor name is required")
	}
	switch m.Type {
	case TypeDocker, TypePing, TypeDNS, TypeHTTPS:
	default:
		return fmt.Errorf("unknown monitor type %q", m.Type)
	}
	if m.AgentID == 0 {
		return fmt.Errorf("an agent must be selected")
	}
	if m.IntervalSec < 10 {
		m.IntervalSec = 60
	}
	if len(m.Config) == 0 {
		m.Config = json.RawMessage("{}")
	}
	if !json.Valid(m.Config) {
		return fmt.Errorf("monitor config is not valid JSON")
	}
	return nil
}

func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.store.ListMonitors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, monitors)
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var m Monitor
	if !decodeBody(w, r, &m) {
		return
	}
	if err := validateMonitor(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetAgent(m.AgentID); err != nil {
		writeErr(w, http.StatusBadRequest, "selected agent does not exist")
		return
	}
	created, err := s.store.CreateMonitor(m)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, created)
}

func (s *Server) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var m Monitor
	if !decodeBody(w, r, &m) {
		return
	}
	m.ID = id
	if err := validateMonitor(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateMonitor(m); err != nil {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	updated, _ := s.store.GetMonitor(id)
	writeJSON(w, updated)
}

func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteMonitor(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleMonitorHeartbeats(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
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
	uptime24h, has24h, _ := s.store.Uptime(id, time.Now().Add(-24*time.Hour).Unix())
	uptime30d, has30d, _ := s.store.Uptime(id, time.Now().Add(-30*24*time.Hour).Unix())
	writeJSON(w, map[string]any{
		"heartbeats": beats,
		"uptime_24h": uptimeOrNil(uptime24h, has24h),
		"uptime_30d": uptimeOrNil(uptime30d, has30d),
	})
}

func uptimeOrNil(v float64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

// ---------- status pages ----------

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validatePage(p *StatusPage) error {
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if !slugRe.MatchString(p.Slug) {
		return fmt.Errorf("slug must be lowercase letters, digits and dashes (e.g. 'main')")
	}
	return nil
}

func (s *Server) handleListStatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.store.ListStatusPages()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, pages)
}

func (s *Server) handleCreateStatusPage(w http.ResponseWriter, r *http.Request) {
	var p StatusPage
	if !decodeBody(w, r, &p) {
		return
	}
	if err := validatePage(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateStatusPage(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not create page (slug already taken?): "+err.Error())
		return
	}
	writeJSON(w, created)
}

func (s *Server) handleUpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var p StatusPage
	if !decodeBody(w, r, &p) {
		return
	}
	p.ID = id
	if err := validatePage(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateStatusPage(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, _ := s.store.GetStatusPage(id)
	writeJSON(w, updated)
}

func (s *Server) handleDeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteStatusPage(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- incidents ----------

func validSeverity(sev string) bool {
	switch sev {
	case "info", "minor", "major", "critical":
		return true
	}
	return false
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	pageID, _ := strconv.ParseInt(r.URL.Query().Get("page_id"), 10, 64)
	incidents, err := s.store.ListIncidents(pageID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, incidents)
}

func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var in Incident
	if !decodeBody(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || in.PageID == 0 {
		writeErr(w, http.StatusBadRequest, "title and page_id are required")
		return
	}
	if !validSeverity(in.Severity) {
		in.Severity = "minor"
	}
	created, err := s.store.CreateIncident(in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, created)
}

func (s *Server) handleUpdateIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in Incident
	if !decodeBody(w, r, &in) {
		return
	}
	in.ID = id
	if !validSeverity(in.Severity) {
		in.Severity = "minor"
	}
	if err := s.store.UpdateIncident(in); err != nil {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	updated, _ := s.store.GetIncident(id)
	writeJSON(w, updated)
}

func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.ResolveIncident(id); err != nil {
		writeErr(w, http.StatusNotFound, "incident not found or already resolved")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleDeleteIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteIncident(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- maintenances ----------

func (s *Server) handleListMaintenances(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListMaintenances()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, list)
}

func validateMaintenance(m *Maintenance) error {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return fmt.Errorf("title is required")
	}
	if m.StartAt == 0 || m.EndAt == 0 || m.EndAt <= m.StartAt {
		return fmt.Errorf("end must be after start")
	}
	return nil
}

func (s *Server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var m Maintenance
	if !decodeBody(w, r, &m) {
		return
	}
	if err := validateMaintenance(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateMaintenance(m)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, created)
}

func (s *Server) handleUpdateMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var m Maintenance
	if !decodeBody(w, r, &m) {
		return
	}
	m.ID = id
	if err := validateMaintenance(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateMaintenance(m); err != nil {
		writeErr(w, http.StatusNotFound, "maintenance not found")
		return
	}
	updated, _ := s.store.GetMaintenance(id)
	writeJSON(w, updated)
}

func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteMaintenance(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- settings ----------

// settableKeys are the settings editable from the UI. session_secret is
// deliberately not in here.
var settableKeys = []string{
	"site_title",
	"discord_webhook_url",
	"smtp_host", "smtp_port", "smtp_user", "smtp_pass", "smtp_from", "smtp_to",
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.AllSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]string{}
	for _, k := range settableKeys {
		out[k] = all[k]
	}
	writeJSON(w, out)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !decodeBody(w, r, &body) {
		return
	}
	for _, k := range settableKeys {
		if v, ok := body[k]; ok {
			if err := s.store.SetSetting(k, strings.TrimSpace(v)); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleTestDiscord(w http.ResponseWriter, r *http.Request) {
	webhook := s.store.GetSetting("discord_webhook_url")
	if webhook == "" {
		writeErr(w, http.StatusBadRequest, "no discord webhook configured (save it first!)")
		return
	}
	err := s.notifier.SendDiscord(webhook, "🧪 TSUNDERE test alert",
		"If you can read this, alerts work. N-not that I doubted myself or anything.", 0xd66ba0)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	err := s.notifier.SendMail("TSUNDERE test alert",
		"If you can read this, mail alerts work. N-not that I doubted myself or anything.")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
