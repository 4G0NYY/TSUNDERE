package server

import (
	"net/http"
	"time"
)

// Bar statuses used by the public status page (per minute-bucket).
const (
	barNoData = -1
	barDown   = 0
	barUp     = 1
)

type publicBar struct {
	Status int   `json:"status"` // -1 no data, 0 down, 1 up
	TS     int64 `json:"ts"`     // bucket start, unix seconds
}

type publicMonitor struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Status    int         `json:"status"` // includes StatusMaintenance
	Uptime30d any         `json:"uptime_30d"`
	Bars      []publicBar `json:"bars"`
}

type publicMaintenance struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	StartAt int64  `json:"start_at"`
	EndAt   int64  `json:"end_at"`
	Active  bool   `json:"active"`
}

// GET /api/status/{slug} — everything a public status page needs, in one call.
func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.GetStatusPageBySlug(r.PathValue("slug"))
	if err != nil || !page.Published {
		writeErr(w, http.StatusNotFound, "no such status page")
		return
	}

	nowTS := time.Now().Unix()
	const barCount = 30
	const bucketSec = 60
	windowStart := nowTS - barCount*bucketSec

	// Maintenances relevant to this page: active now, or starting within 7 days.
	allMaint, err := s.store.ListMaintenances()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	pageMonitorSet := map[int64]bool{}
	for _, id := range page.MonitorIDs {
		pageMonitorSet[id] = true
	}
	maintOut := []publicMaintenance{}
	monitorInMaint := map[int64]bool{}
	for _, m := range allMaint {
		touchesPage := false
		for _, mid := range m.MonitorIDs {
			if pageMonitorSet[mid] {
				touchesPage = true
				if m.ActiveAt(nowTS) {
					monitorInMaint[mid] = true
				}
			}
		}
		if !touchesPage {
			continue
		}
		if m.EndAt >= nowTS && m.StartAt <= nowTS+7*24*3600 {
			maintOut = append(maintOut, publicMaintenance{
				Title: m.Title, Body: m.Body, StartAt: m.StartAt, EndAt: m.EndAt, Active: m.ActiveAt(nowTS),
			})
		}
	}

	monitors := []publicMonitor{}
	overall := "up"
	anyDown, anyMaint := false, false
	for _, id := range page.MonitorIDs {
		m, err := s.store.GetMonitor(id)
		if err != nil {
			continue // monitor deleted but still referenced
		}
		beats, err := s.store.HeartbeatsSince(id, windowStart)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Bucket heartbeats into 1-minute bars; any down beat taints the bucket.
		bars := make([]publicBar, barCount)
		for i := range bars {
			bars[i] = publicBar{Status: barNoData, TS: windowStart + int64(i*bucketSec)}
		}
		for _, hb := range beats {
			idx := int((hb.CheckedAt - windowStart) / bucketSec)
			if idx < 0 || idx >= barCount {
				continue
			}
			if hb.Status == StatusDown {
				bars[idx].Status = barDown
			} else if bars[idx].Status != barDown {
				bars[idx].Status = barUp
			}
		}

		uptime, hasUptime, _ := s.store.Uptime(id, nowTS-30*24*3600)
		status := m.LastStatus
		if monitorInMaint[id] {
			status = StatusMaintenance
			anyMaint = true
		} else if status == StatusDown {
			anyDown = true
		}
		monitors = append(monitors, publicMonitor{
			ID: m.ID, Name: m.Name, Status: status,
			Uptime30d: uptimeOrNil(uptime, hasUptime),
			Bars:      bars,
		})
	}
	if anyDown {
		overall = "down"
	} else if anyMaint {
		overall = "maintenance"
	}

	incidents, err := s.store.ListIncidents(page.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Open incidents, plus incidents resolved in the last 24h for context.
	incOut := []Incident{}
	for _, in := range incidents {
		if in.ResolvedAt == 0 || nowTS-in.ResolvedAt < 24*3600 {
			incOut = append(incOut, in)
		}
	}
	if len(incOut) > 0 && overall == "up" {
		for _, in := range incOut {
			if in.ResolvedAt == 0 {
				overall = "degraded"
				break
			}
		}
	}

	writeJSON(w, map[string]any{
		"page": map[string]any{
			"slug":        page.Slug,
			"title":       page.Title,
			"description": page.Description,
		},
		"overall":      overall,
		"monitors":     monitors,
		"incidents":    incOut,
		"maintenances": maintOut,
		"generated_at": nowTS,
	})
}
