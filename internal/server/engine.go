package server

import (
	"log"
	"time"
)

// Engine turns raw check results into heartbeats, tracks status transitions,
// fires alerts (unless the monitor is inside a maintenance window), marks
// monitors down when their agent stops reporting, and prunes old heartbeats.
type Engine struct {
	store    *Store
	notifier *Notifier
}

func NewEngine(store *Store, notifier *Notifier) *Engine {
	return &Engine{store: store, notifier: notifier}
}

// ProcessResult ingests one check result from an agent (or a synthetic one
// from the stale detector). Callers pass the already-loaded monitor so it is
// not fetched twice per result.
func (e *Engine) ProcessResult(m Monitor, res CheckResult) {
	if res.CheckedAt == 0 {
		res.CheckedAt = time.Now().Unix()
	}
	if res.Status != StatusUp {
		res.Status = StatusDown
	}
	hb := Heartbeat{
		MonitorID: m.ID,
		Status:    res.Status,
		LatencyMS: res.LatencyMS,
		Message:   res.Message,
		CheckedAt: res.CheckedAt,
	}
	if err := e.store.InsertHeartbeat(hb); err != nil {
		log.Printf("engine: insert heartbeat: %v", err)
		return
	}
	if m.LastStatus == res.Status {
		return
	}

	if err := e.store.SetMonitorStatus(m.ID, res.Status, res.CheckedAt); err != nil {
		log.Printf("engine: update monitor status: %v", err)
	}

	// Never alert on the very first result (pending -> anything); only real flips.
	if m.LastStatus == StatusPending {
		return
	}
	inMaint, err := e.store.MonitorInActiveMaintenance(m.ID)
	if err != nil {
		log.Printf("engine: maintenance lookup: %v", err)
	}
	if inMaint {
		log.Printf("engine: %s flipped to %d during maintenance, alert suppressed", m.Name, res.Status)
		return
	}
	go e.notifier.NotifyStatusChange(m, hb)
}

// Run starts the background loops: stale detection and heartbeat retention.
func (e *Engine) Run() {
	go e.staleLoop()
	go e.pruneLoop()
}

// staleLoop marks monitors down when no heartbeat arrived for ~2 intervals,
// which covers both dead agents and agents that silently dropped a monitor.
func (e *Engine) staleLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		stale, err := e.store.StaleMonitorCandidates()
		if err != nil {
			log.Printf("engine: stale scan: %v", err)
			continue
		}
		for _, m := range stale {
			e.ProcessResult(m, CheckResult{
				MonitorID: m.ID,
				Status:    StatusDown,
				Message:   "no data received from agent (agent offline?)",
			})
		}
	}
}

// pruneLoop deletes heartbeats older than 90 days once an hour.
func (e *Engine) pruneLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-90 * 24 * time.Hour).Unix()
		if n, err := e.store.PruneHeartbeats(cutoff); err != nil {
			log.Printf("engine: prune: %v", err)
		} else if n > 0 {
			log.Printf("engine: pruned %d old heartbeats", n)
		}
	}
}
