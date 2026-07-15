package server

import "encoding/json"

// Monitor status values. Stored on heartbeats and monitors.last_status.
const (
	StatusDown        = 0
	StatusUp          = 1
	StatusPending     = 2 // no data yet
	StatusMaintenance = 3 // only ever computed for display, never stored
)

// Monitor types executed by agents.
const (
	TypeDocker = "docker"
	TypePing   = "ping"
	TypeDNS    = "dns"
	TypeHTTPS  = "https"
)

type Agent struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Hostname  string          `json:"hostname"`  // OS hostname self-reported by the agent
	LastSeen  int64           `json:"last_seen"` // unix seconds, 0 = never
	Inventory json.RawMessage `json:"inventory"`
	CreatedAt int64           `json:"created_at"`
	Online    bool            `json:"online"`
}

// DockerInventory is what agents report about their local Docker daemon,
// so the admin UI can offer a dropdown when mapping a docker monitor.
type DockerInventory struct {
	Containers []string `json:"containers"`
	Services   []string `json:"services"`
	UpdatedAt  int64    `json:"updated_at"`
}

type Monitor struct {
	ID            int64           `json:"id"`
	AgentID       int64           `json:"agent_id"`
	AgentName     string          `json:"agent_name,omitempty"`
	AgentHostname string          `json:"agent_hostname,omitempty"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Config        json.RawMessage `json:"config"`
	IntervalSec   int             `json:"interval_sec"`
	Enabled       bool            `json:"enabled"`
	LastStatus    int             `json:"last_status"`
	LastChange    int64           `json:"last_change"`
	CreatedAt     int64           `json:"created_at"`
}

type Heartbeat struct {
	ID        int64   `json:"id"`
	MonitorID int64   `json:"monitor_id"`
	Status    int     `json:"status"`
	LatencyMS float64 `json:"latency_ms"`
	Message   string  `json:"message"`
	CheckedAt int64   `json:"checked_at"`
}

type StatusPage struct {
	ID            int64   `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Published     bool    `json:"published"`
	ShowHostnames bool    `json:"show_hostnames"` // show the agent hostname under each monitor
	CreatedAt     int64   `json:"created_at"`
	MonitorIDs    []int64 `json:"monitor_ids"`
}

type Incident struct {
	ID         int64  `json:"id"`
	PageID     int64  `json:"page_id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Severity   string `json:"severity"` // info | minor | major | critical
	CreatedAt  int64  `json:"created_at"`
	ResolvedAt int64  `json:"resolved_at"` // 0 = still open
}

type Maintenance struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	StartAt    int64   `json:"start_at"`
	EndAt      int64   `json:"end_at"`
	CreatedAt  int64   `json:"created_at"`
	MonitorIDs []int64 `json:"monitor_ids"`
}

func (m Maintenance) ActiveAt(now int64) bool {
	return now >= m.StartAt && now <= m.EndAt
}

// AgentMonitor is the trimmed-down monitor definition sent to agents.
type AgentMonitor struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	IntervalSec int             `json:"interval_sec"`
	Config      json.RawMessage `json:"config"`
}

// CheckResult is what agents POST back after running a check.
type CheckResult struct {
	MonitorID int64   `json:"monitor_id"`
	Status    int     `json:"status"`
	LatencyMS float64 `json:"latency_ms"`
	Message   string  `json:"message"`
	CheckedAt int64   `json:"checked_at"` // optional; server fills if 0
}
