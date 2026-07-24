package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

// ---------- API keys ----------
//
// Keys authenticate the read-only /api/v1 surface. Same hygiene as agent
// tokens: generate a random secret, hand back the plaintext exactly once, and
// only ever persist its SHA-256 hash.

func newAPIKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// "tsk_" = TSUNDERE key, so it never gets confused with a "tsa_" agent token.
	return "tsk_" + hex.EncodeToString(buf), nil
}

const apiKeyCols = `id, name, prefix, last_used, created_at`

func scanAPIKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var k APIKey
	if err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.LastUsed, &k.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return k, ErrNotFound
		}
		return k, err
	}
	return k, nil
}

func (s *Store) CreateAPIKey(name string) (APIKey, string, error) {
	key, err := newAPIKey()
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := key[:12] // "tsk_" + 8 hex chars — enough to recognise, useless to abuse
	res, err := s.db.Exec(`INSERT INTO api_keys (name, prefix, key_hash, created_at) VALUES (?, ?, ?, ?)`,
		name, prefix, hashToken(key), now())
	if err != nil {
		return APIKey{}, "", err
	}
	id, _ := res.LastInsertId()
	k, err := s.GetAPIKey(id)
	return k, key, err
}

func (s *Store) GetAPIKey(id int64) (APIKey, error) {
	return scanAPIKey(s.db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE id = ?`, id))
}

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	rows, err := s.db.Query(`SELECT ` + apiKeyCols + ` FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIKey(id int64) error {
	res, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ValidateAPIKey resolves a plaintext key to its record and bumps last_used.
// Returns ErrNotFound for an unknown key.
func (s *Store) ValidateAPIKey(key string) (APIKey, error) {
	k, err := scanAPIKey(s.db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE key_hash = ?`, hashToken(key)))
	if err != nil {
		return k, err
	}
	// Best-effort; a failed touch shouldn't deny an otherwise valid key.
	s.db.Exec(`UPDATE api_keys SET last_used = ? WHERE id = ?`, now(), k.ID)
	return k, nil
}

// ---------- read-only event feed ----------

// RecentEvents returns the most recent heartbeats across all monitors, joined
// with monitor/agent identity, for the log-feed widget. A status of -1 means
// "any"; otherwise it filters to that status (0 down, 1 up, 2 pending).
func (s *Store) RecentEvents(limit, status int) ([]EventLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT h.monitor_id, m.name, m.type, a.name, a.hostname, h.status, h.latency_ms, h.message, h.checked_at
		FROM heartbeats h
		JOIN monitors m ON m.id = h.monitor_id
		JOIN agents a ON a.id = m.agent_id`
	args := []any{}
	if status >= 0 {
		q += ` WHERE h.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY h.checked_at DESC, h.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventLog{}
	for rows.Next() {
		var e EventLog
		if err := rows.Scan(&e.MonitorID, &e.MonitorName, &e.Type, &e.AgentName, &e.AgentHostname,
			&e.Status, &e.LatencyMS, &e.Message, &e.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
