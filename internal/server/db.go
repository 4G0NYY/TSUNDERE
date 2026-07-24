package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

// Store wraps the sqlite database. All timestamps are stored as unix seconds
// (INTEGER) to sidestep driver-specific time formatting.
type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single connection: sqlite has one writer anyway and this avoids
	// SQLITE_BUSY at the scale this tool runs at.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  hostname   TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL UNIQUE,
  last_seen  INTEGER NOT NULL DEFAULT 0,
  inventory  TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS monitors (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id     INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  type         TEXT NOT NULL,
  name         TEXT NOT NULL,
  config       TEXT NOT NULL DEFAULT '{}',
  interval_sec INTEGER NOT NULL DEFAULT 60,
  enabled      INTEGER NOT NULL DEFAULT 1,
  last_status  INTEGER NOT NULL DEFAULT 2,
  last_change  INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS heartbeats (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  status     INTEGER NOT NULL,
  latency_ms REAL NOT NULL DEFAULT 0,
  message    TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_heartbeats_monitor_time ON heartbeats(monitor_id, checked_at);
CREATE INDEX IF NOT EXISTS idx_heartbeats_time ON heartbeats(checked_at);
CREATE TABLE IF NOT EXISTS status_pages (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  slug           TEXT NOT NULL UNIQUE,
  title          TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  published      INTEGER NOT NULL DEFAULT 1,
  show_hostnames INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS status_page_monitors (
  page_id    INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  sort       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (page_id, monitor_id)
);
CREATE TABLE IF NOT EXISTS incidents (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  page_id     INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  severity    TEXT NOT NULL DEFAULT 'minor',
  created_at  INTEGER NOT NULL,
  resolved_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS maintenances (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  start_at   INTEGER NOT NULL,
  end_at     INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS maintenance_monitors (
  maintenance_id INTEGER NOT NULL REFERENCES maintenances(id) ON DELETE CASCADE,
  monitor_id     INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  PRIMARY KEY (maintenance_id, monitor_id)
);
CREATE TABLE IF NOT EXISTS api_keys (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  prefix     TEXT NOT NULL DEFAULT '',
  key_hash   TEXT NOT NULL UNIQUE,
  last_used  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
`)
	if err != nil {
		return err
	}
	// Additive migrations for databases created before these columns existed.
	if err := s.ensureColumn("agents", "hostname", `hostname TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return s.ensureColumn("status_pages", "show_hostnames", `show_hostnames INTEGER NOT NULL DEFAULT 0`)
}

// ensureColumn adds a column when it is missing; CREATE TABLE IF NOT EXISTS
// alone never upgrades tables that already exist.
func (s *Store) ensureColumn(table, column, ddl string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s`, table, ddl))
	return err
}

func now() int64 { return time.Now().Unix() }

// ---------- settings ----------

func (s *Store) GetSetting(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SessionSecret returns the persisted HMAC secret, generating one on first run.
func (s *Store) SessionSecret() ([]byte, error) {
	if v := s.GetSetting("session_secret"); v != "" {
		return hex.DecodeString(v)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := s.SetSetting("session_secret", hex.EncodeToString(buf)); err != nil {
		return nil, err
	}
	return buf, nil
}

// ---------- agents ----------

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "tsa_" + hex.EncodeToString(buf), nil
}

// agentOnlineWindow: an agent is "online" if seen within this many seconds.
const agentOnlineWindow = 90

func (s *Store) CreateAgent(name string) (Agent, string, error) {
	token, err := newToken()
	if err != nil {
		return Agent{}, "", err
	}
	res, err := s.db.Exec(`INSERT INTO agents (name, token_hash, inventory, created_at) VALUES (?, ?, '{}', ?)`,
		name, hashToken(token), now())
	if err != nil {
		return Agent{}, "", err
	}
	id, _ := res.LastInsertId()
	a, err := s.GetAgent(id)
	return a, token, err
}

func (s *Store) RegenerateAgentToken(id int64) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.Exec(`UPDATE agents SET token_hash = ? WHERE id = ?`, hashToken(token), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

func scanAgent(row interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var inv string
	if err := row.Scan(&a.ID, &a.Name, &a.Hostname, &a.LastSeen, &inv, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a, ErrNotFound
		}
		return a, err
	}
	a.Inventory = json.RawMessage(inv)
	a.Online = a.LastSeen > 0 && now()-a.LastSeen <= agentOnlineWindow
	return a, nil
}

const agentCols = `id, name, hostname, last_seen, inventory, created_at`

func (s *Store) GetAgent(id int64) (Agent, error) {
	return scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE id = ?`, id))
}

func (s *Store) GetAgentByToken(token string) (Agent, error) {
	return scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE token_hash = ?`, hashToken(token)))
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.db.Query(`SELECT ` + agentCols + ` FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TouchAgent bumps last_seen and, when the agent reported one, its hostname —
// one write either way, since this runs on every agent request.
func (s *Store) TouchAgent(id int64, hostname string) error {
	if hostname != "" {
		_, err := s.db.Exec(`UPDATE agents SET last_seen = ?, hostname = ? WHERE id = ?`, now(), hostname, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE agents SET last_seen = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) UpdateAgentInventory(id int64, inv DockerInventory) error {
	inv.UpdatedAt = now()
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE agents SET inventory = ? WHERE id = ?`, string(b), id)
	return err
}

func (s *Store) RenameAgent(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE agents SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAgent(id int64) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE id = ?`, id)
	return err
}

// ---------- monitors ----------

func scanMonitor(row interface{ Scan(...any) error }) (Monitor, error) {
	var m Monitor
	var cfg string
	var enabled int
	if err := row.Scan(&m.ID, &m.AgentID, &m.Type, &m.Name, &cfg, &m.IntervalSec, &enabled, &m.LastStatus, &m.LastChange, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return m, ErrNotFound
		}
		return m, err
	}
	m.Config = json.RawMessage(cfg)
	m.Enabled = enabled == 1
	return m, nil
}

const monitorCols = `id, agent_id, type, name, config, interval_sec, enabled, last_status, last_change, created_at`

func (s *Store) CreateMonitor(m Monitor) (Monitor, error) {
	res, err := s.db.Exec(`INSERT INTO monitors (agent_id, type, name, config, interval_sec, enabled, last_status, last_change, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		m.AgentID, m.Type, m.Name, string(m.Config), m.IntervalSec, boolInt(m.Enabled), StatusPending, now())
	if err != nil {
		return Monitor{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetMonitor(id)
}

func (s *Store) UpdateMonitor(m Monitor) error {
	res, err := s.db.Exec(`UPDATE monitors SET agent_id = ?, type = ?, name = ?, config = ?, interval_sec = ?, enabled = ? WHERE id = ?`,
		m.AgentID, m.Type, m.Name, string(m.Config), m.IntervalSec, boolInt(m.Enabled), m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetMonitorStatus(id int64, status int, changedAt int64) error {
	_, err := s.db.Exec(`UPDATE monitors SET last_status = ?, last_change = ? WHERE id = ?`, status, changedAt, id)
	return err
}

func (s *Store) GetMonitor(id int64) (Monitor, error) {
	return scanMonitor(s.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE id = ?`, id))
}

func scanMonitorWithAgent(rows *sql.Rows) (Monitor, error) {
	var m Monitor
	var cfg string
	var enabled int
	if err := rows.Scan(&m.ID, &m.AgentID, &m.Type, &m.Name, &cfg, &m.IntervalSec, &enabled, &m.LastStatus, &m.LastChange, &m.CreatedAt, &m.AgentName, &m.AgentHostname); err != nil {
		return m, err
	}
	m.Config = json.RawMessage(cfg)
	m.Enabled = enabled == 1
	return m, nil
}

const monitorAgentCols = `m.id, m.agent_id, m.type, m.name, m.config, m.interval_sec, m.enabled, m.last_status, m.last_change, m.created_at, a.name, a.hostname`

func (s *Store) ListMonitors() ([]Monitor, error) {
	rows, err := s.db.Query(`SELECT ` + monitorAgentCols + `
		FROM monitors m JOIN agents a ON a.id = m.agent_id ORDER BY m.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitorWithAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MonitorsByIDs returns the monitors (with agent name/hostname) keyed by id,
// in a single query. Ids that no longer exist are simply absent.
func (s *Store) MonitorsByIDs(ids []int64) (map[int64]Monitor, error) {
	out := map[int64]Monitor{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT `+monitorAgentCols+`
		FROM monitors m JOIN agents a ON a.id = m.agent_id WHERE m.id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMonitorWithAgent(rows)
		if err != nil {
			return nil, err
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func (s *Store) ListMonitorsForAgent(agentID int64) ([]Monitor, error) {
	rows, err := s.db.Query(`SELECT `+monitorCols+` FROM monitors WHERE agent_id = ? AND enabled = 1`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMonitor(id int64) error {
	_, err := s.db.Exec(`DELETE FROM monitors WHERE id = ?`, id)
	return err
}

// ---------- heartbeats ----------

func (s *Store) InsertHeartbeat(hb Heartbeat) error {
	_, err := s.db.Exec(`INSERT INTO heartbeats (monitor_id, status, latency_ms, message, checked_at) VALUES (?, ?, ?, ?, ?)`,
		hb.MonitorID, hb.Status, hb.LatencyMS, hb.Message, hb.CheckedAt)
	return err
}

func (s *Store) LastHeartbeat(monitorID int64) (Heartbeat, error) {
	var hb Heartbeat
	err := s.db.QueryRow(`SELECT id, monitor_id, status, latency_ms, message, checked_at FROM heartbeats
		WHERE monitor_id = ? ORDER BY checked_at DESC, id DESC LIMIT 1`, monitorID).
		Scan(&hb.ID, &hb.MonitorID, &hb.Status, &hb.LatencyMS, &hb.Message, &hb.CheckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return hb, ErrNotFound
	}
	return hb, err
}

func (s *Store) HeartbeatsSince(monitorID int64, since int64) ([]Heartbeat, error) {
	rows, err := s.db.Query(`SELECT id, monitor_id, status, latency_ms, message, checked_at FROM heartbeats
		WHERE monitor_id = ? AND checked_at >= ? ORDER BY checked_at`, monitorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Heartbeat{}
	for rows.Next() {
		var hb Heartbeat
		if err := rows.Scan(&hb.ID, &hb.MonitorID, &hb.Status, &hb.LatencyMS, &hb.Message, &hb.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// HeartbeatsForMonitorsSince returns heartbeats per monitor since the cutoff —
// one query for a whole status page instead of one per monitor.
func (s *Store) HeartbeatsForMonitorsSince(ids []int64, since int64) (map[int64][]Heartbeat, error) {
	out := map[int64][]Heartbeat{}
	if len(ids) == 0 {
		return out, nil
	}
	args := append(int64Args(ids), since)
	rows, err := s.db.Query(`SELECT id, monitor_id, status, latency_ms, message, checked_at FROM heartbeats
		WHERE monitor_id IN (`+placeholders(len(ids))+`) AND checked_at >= ? ORDER BY checked_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hb Heartbeat
		if err := rows.Scan(&hb.ID, &hb.MonitorID, &hb.Status, &hb.LatencyMS, &hb.Message, &hb.CheckedAt); err != nil {
			return nil, err
		}
		out[hb.MonitorID] = append(out[hb.MonitorID], hb)
	}
	return out, rows.Err()
}

// UptimesSince returns the up-ratio per monitor over the window in one query.
// Monitors with no data in the window are absent from the map.
func (s *Store) UptimesSince(ids []int64, since int64) (map[int64]float64, error) {
	out := map[int64]float64{}
	if len(ids) == 0 {
		return out, nil
	}
	args := append(int64Args(ids), since)
	rows, err := s.db.Query(`SELECT monitor_id, COUNT(*), SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END)
		FROM heartbeats WHERE monitor_id IN (`+placeholders(len(ids))+`) AND checked_at >= ? AND status IN (0, 1)
		GROUP BY monitor_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, total, up int64
		if err := rows.Scan(&id, &total, &up); err != nil {
			return nil, err
		}
		if total > 0 {
			out[id] = float64(up) / float64(total)
		}
	}
	return out, rows.Err()
}

// Uptime returns the up-ratio (0..1) over the window, and whether any data exists.
func (s *Store) Uptime(monitorID int64, since int64) (float64, bool, error) {
	var total, up int64
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0)
		FROM heartbeats WHERE monitor_id = ? AND checked_at >= ? AND status IN (0, 1)`, monitorID, since).
		Scan(&total, &up)
	if err != nil || total == 0 {
		return 0, false, err
	}
	return float64(up) / float64(total), true, nil
}

func (s *Store) PruneHeartbeats(olderThan int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM heartbeats WHERE checked_at < ?`, olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StaleMonitorCandidates returns enabled monitors whose last heartbeat is older
// than roughly two intervals (plus grace), and that are not already marked down.
func (s *Store) StaleMonitorCandidates() ([]Monitor, error) {
	rows, err := s.db.Query(`SELECT `+monitorCols+` FROM monitors m
		WHERE m.enabled = 1 AND m.last_status != 0
		AND COALESCE((SELECT MAX(checked_at) FROM heartbeats h WHERE h.monitor_id = m.id), m.created_at) < ? - (m.interval_sec * 2 + 30)`,
		now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- status pages ----------

func (s *Store) pageMonitorIDs(pageID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT monitor_id FROM status_page_monitors WHERE page_id = ? ORDER BY sort`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) setPageMonitors(pageID int64, monitorIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM status_page_monitors WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	for i, mid := range monitorIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO status_page_monitors (page_id, monitor_id, sort) VALUES (?, ?, ?)`, pageID, mid, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateStatusPage(p StatusPage) (StatusPage, error) {
	res, err := s.db.Exec(`INSERT INTO status_pages (slug, title, description, published, show_hostnames, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Title, p.Description, boolInt(p.Published), boolInt(p.ShowHostnames), now())
	if err != nil {
		return StatusPage{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.setPageMonitors(id, p.MonitorIDs); err != nil {
		return StatusPage{}, err
	}
	return s.GetStatusPage(id)
}

func (s *Store) UpdateStatusPage(p StatusPage) error {
	res, err := s.db.Exec(`UPDATE status_pages SET slug = ?, title = ?, description = ?, published = ?, show_hostnames = ? WHERE id = ?`,
		p.Slug, p.Title, p.Description, boolInt(p.Published), boolInt(p.ShowHostnames), p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.setPageMonitors(p.ID, p.MonitorIDs)
}

const statusPageCols = `id, slug, title, description, published, show_hostnames, created_at`

func (s *Store) scanStatusPage(row interface{ Scan(...any) error }) (StatusPage, error) {
	var p StatusPage
	var published, showHost int
	if err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &published, &showHost, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	p.Published = published == 1
	p.ShowHostnames = showHost == 1
	var err error
	p.MonitorIDs, err = s.pageMonitorIDs(p.ID)
	return p, err
}

func (s *Store) GetStatusPage(id int64) (StatusPage, error) {
	return s.scanStatusPage(s.db.QueryRow(`SELECT `+statusPageCols+` FROM status_pages WHERE id = ?`, id))
}

func (s *Store) GetStatusPageBySlug(slug string) (StatusPage, error) {
	return s.scanStatusPage(s.db.QueryRow(`SELECT `+statusPageCols+` FROM status_pages WHERE slug = ?`, slug))
}

func (s *Store) ListStatusPages() ([]StatusPage, error) {
	rows, err := s.db.Query(`SELECT ` + statusPageCols + ` FROM status_pages ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Collect first: pageMonitorIDs runs its own query, which would clash with an open rows cursor.
	type raw struct {
		p                   StatusPage
		published, showHost int
	}
	raws := []raw{}
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.p.ID, &r.p.Slug, &r.p.Title, &r.p.Description, &r.published, &r.showHost, &r.p.CreatedAt); err != nil {
			return nil, err
		}
		raws = append(raws, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := []StatusPage{}
	for _, r := range raws {
		r.p.Published = r.published == 1
		r.p.ShowHostnames = r.showHost == 1
		ids, err := s.pageMonitorIDs(r.p.ID)
		if err != nil {
			return nil, err
		}
		r.p.MonitorIDs = ids
		out = append(out, r.p)
	}
	return out, nil
}

func (s *Store) DeleteStatusPage(id int64) error {
	_, err := s.db.Exec(`DELETE FROM status_pages WHERE id = ?`, id)
	return err
}

// ---------- incidents ----------

func (s *Store) CreateIncident(in Incident) (Incident, error) {
	res, err := s.db.Exec(`INSERT INTO incidents (page_id, title, body, severity, created_at, resolved_at) VALUES (?, ?, ?, ?, ?, 0)`,
		in.PageID, in.Title, in.Body, in.Severity, now())
	if err != nil {
		return Incident{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetIncident(id)
}

func (s *Store) UpdateIncident(in Incident) error {
	res, err := s.db.Exec(`UPDATE incidents SET title = ?, body = ?, severity = ? WHERE id = ?`, in.Title, in.Body, in.Severity, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ResolveIncident(id int64) error {
	res, err := s.db.Exec(`UPDATE incidents SET resolved_at = ? WHERE id = ? AND resolved_at = 0`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetIncident(id int64) (Incident, error) {
	var in Incident
	err := s.db.QueryRow(`SELECT id, page_id, title, body, severity, created_at, resolved_at FROM incidents WHERE id = ?`, id).
		Scan(&in.ID, &in.PageID, &in.Title, &in.Body, &in.Severity, &in.CreatedAt, &in.ResolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	return in, err
}

func (s *Store) ListIncidents(pageID int64) ([]Incident, error) {
	q := `SELECT id, page_id, title, body, severity, created_at, resolved_at FROM incidents`
	args := []any{}
	if pageID > 0 {
		q += ` WHERE page_id = ?`
		args = append(args, pageID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		if err := rows.Scan(&in.ID, &in.PageID, &in.Title, &in.Body, &in.Severity, &in.CreatedAt, &in.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIncident(id int64) error {
	_, err := s.db.Exec(`DELETE FROM incidents WHERE id = ?`, id)
	return err
}

// ---------- maintenances ----------

func (s *Store) maintenanceMonitorIDs(mid int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT monitor_id FROM maintenance_monitors WHERE maintenance_id = ?`, mid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) setMaintenanceMonitors(mid int64, monitorIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM maintenance_monitors WHERE maintenance_id = ?`, mid); err != nil {
		return err
	}
	for _, id := range monitorIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO maintenance_monitors (maintenance_id, monitor_id) VALUES (?, ?)`, mid, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateMaintenance(m Maintenance) (Maintenance, error) {
	res, err := s.db.Exec(`INSERT INTO maintenances (title, body, start_at, end_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		m.Title, m.Body, m.StartAt, m.EndAt, now())
	if err != nil {
		return Maintenance{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.setMaintenanceMonitors(id, m.MonitorIDs); err != nil {
		return Maintenance{}, err
	}
	return s.GetMaintenance(id)
}

func (s *Store) UpdateMaintenance(m Maintenance) error {
	res, err := s.db.Exec(`UPDATE maintenances SET title = ?, body = ?, start_at = ?, end_at = ? WHERE id = ?`,
		m.Title, m.Body, m.StartAt, m.EndAt, m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.setMaintenanceMonitors(m.ID, m.MonitorIDs)
}

func (s *Store) GetMaintenance(id int64) (Maintenance, error) {
	var m Maintenance
	err := s.db.QueryRow(`SELECT id, title, body, start_at, end_at, created_at FROM maintenances WHERE id = ?`, id).
		Scan(&m.ID, &m.Title, &m.Body, &m.StartAt, &m.EndAt, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.MonitorIDs, err = s.maintenanceMonitorIDs(id)
	return m, err
}

func (s *Store) ListMaintenances() ([]Maintenance, error) {
	rows, err := s.db.Query(`SELECT id, title, body, start_at, end_at, created_at FROM maintenances ORDER BY start_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raws := []Maintenance{}
	for rows.Next() {
		var m Maintenance
		if err := rows.Scan(&m.ID, &m.Title, &m.Body, &m.StartAt, &m.EndAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		raws = append(raws, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := []Maintenance{}
	for _, m := range raws {
		ids, err := s.maintenanceMonitorIDs(m.ID)
		if err != nil {
			return nil, err
		}
		m.MonitorIDs = ids
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) DeleteMaintenance(id int64) error {
	_, err := s.db.Exec(`DELETE FROM maintenances WHERE id = ?`, id)
	return err
}

// MonitorInActiveMaintenance reports whether the monitor is covered by a
// maintenance window active right now (used to pause alerts).
func (s *Store) MonitorInActiveMaintenance(monitorID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM maintenances m
		JOIN maintenance_monitors mm ON mm.maintenance_id = m.id
		WHERE mm.monitor_id = ? AND m.start_at <= ? AND m.end_at >= ?`,
		monitorID, now(), now()).Scan(&n)
	return n > 0, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
