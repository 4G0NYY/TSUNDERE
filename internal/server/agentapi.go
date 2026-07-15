package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// agentAuth resolves the Bearer token to an agent and updates last_seen.
func (s *Server) agentAuth(w http.ResponseWriter, r *http.Request) (Agent, bool) {
	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || token == "" {
		writeErr(w, http.StatusUnauthorized, "missing agent token")
		return Agent{}, false
	}
	agent, err := s.store.GetAgentByToken(strings.TrimSpace(token))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid agent token")
		return Agent{}, false
	}
	s.store.TouchAgent(agent.ID)
	return agent, true
}

// GET /api/agent/config — the monitors this agent should run.
func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	monitors, err := s.store.ListMonitorsForAgent(agent.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]AgentMonitor, 0, len(monitors))
	for _, m := range monitors {
		out = append(out, AgentMonitor{ID: m.ID, Type: m.Type, IntervalSec: m.IntervalSec, Config: m.Config})
	}
	writeJSON(w, map[string]any{"agent_name": agent.Name, "monitors": out})
}

// POST /api/agent/results — batch of check results.
func (s *Server) handleAgentResults(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		Results []CheckResult `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	accepted := 0
	for _, res := range body.Results {
		// Only accept results for monitors that actually belong to this agent.
		m, err := s.store.GetMonitor(res.MonitorID)
		if err != nil || m.AgentID != agent.ID {
			continue
		}
		s.engine.ProcessResult(res)
		accepted++
	}
	writeJSON(w, map[string]any{"accepted": accepted})
}

// POST /api/agent/inventory — docker containers/services visible to the agent.
func (s *Server) handleAgentInventory(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	var inv DockerInventory
	if err := json.NewDecoder(r.Body).Decode(&inv); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.store.UpdateAgentInventory(agent.ID, inv); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
