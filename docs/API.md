# TSUNDERE read-only API (v1)

A small, **strictly read-only** HTTP surface for external dashboards (e.g. the
[TSUNDERE Portal](https://github.com/4G0NYY/dashboard)). There is deliberately
no endpoint here that changes state — only `GET` (and CORS `OPTIONS`) are
accepted; anything else returns `405`.

## Authentication

Every request needs an **API key**, created under **API keys** in the admin UI.
The plaintext key (prefixed `tsk_`) is shown exactly once; only its SHA-256 hash
is stored. Send it either way:

```
X-API-Key: tsk_xxxxxxxx…
# or
Authorization: Bearer tsk_xxxxxxxx…
```

In `TSUNDERE_DEV_NO_AUTH=1` mode the key check is skipped, matching the admin
bypass, so the dashboard can be developed against a local server.

CORS is open (`Access-Control-Allow-Origin: *`) so a browser SPA on another
origin (Cloudflare Pages/Workers) can call it directly. In production the
Portal proxies through its Worker and keeps the key server-side.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/status` | Health summary: monitors up/down/pending, agents online, overall. |
| GET | `/api/v1/nodes` | Monitored hosts (agents), sanitised — no tokens. |
| GET | `/api/v1/monitors` | All monitors with current status, type and target. |
| GET | `/api/v1/metrics` | Per-monitor latency + 24h/30d uptime snapshot. |
| GET | `/api/v1/monitors/{id}/heartbeats?hours=N` | Raw time-series for one monitor (sparklines/graphs). `hours` 1…2160, default 24. |
| GET | `/api/v1/logs?severity=up\|down\|pending&limit=N` | Recent events across all monitors, newest first. `limit` ≤ 500, default 100. |

### Status codes

- `200` — OK
- `401` — missing/invalid API key
- `404` — unknown monitor id
- `405` — a non-GET method was used against the read-only API

### Example

```bash
curl -s -H "X-API-Key: tsk_…" https://status.example.com/api/v1/status | jq
```

```json
{
  "overall": "up",
  "monitors_total": 12,
  "monitors_up": 12,
  "monitors_down": 0,
  "monitors_pending": 0,
  "agents_total": 3,
  "agents_online": 3,
  "generated_at": 1753382400
}
```
