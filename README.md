# 💢 TSUNDERE

**T**elemetry, **S**ystem **U**ptime, & **N**etwork **D**ata **E**ngine for **R**apid **E**valuation

A lightweight, agent-based monitoring system. It's not like it was built because Uptime Kuma
chokes on many collection points or anything… b-baka.

- **Tiny footprint** — two static Go binaries + SQLite. Runs happily on the cheapest VPS you can find.
- **Agent-based** — agents *pull* their monitor config from the server and *push* results back.
  No inbound ports on monitored hosts, only outbound HTTPS.
- **Dark mode only.** No flashbangs. Ever.

## What it monitors

| Type | What it does |
|---|---|
| `docker` | Container running/healthy, or Swarm service task count vs. desired replicas — via `docker.sock` |
| `ping` | ICMP ping to an IP/host (uses the system `ping`, no root needed) |
| `dns` | Queries a name against a *specific* DNS server (A/AAAA/CNAME/MX/NS/TXT, optional expected answer) |
| `https` | HTTP(S) check with status code, optional keyword match, and TLS certificate expiry detection |

## Server features

- **Status pages** like Uptime Kuma's: friendly name, 30-day uptime percentage, and green
  bars for the last 30 minutes. Create as many pages as you like, each with its own monitor
  selection, at `/status/<slug>`. A per-page "Display host name" option shows the agent's
  hostname under each monitor — handy when several hosts run identically named containers
  (e.g. a PG-HA cluster).
- **Incidents** — publish/resolve incidents per status page (info/minor/major/critical).
- **Maintenance windows** — schedule them, pick the affected monitors; their alerts are
  paused during the window and the window is announced on the status page.
- **Alerts** via Discord webhook and/or SMTP mail on every up/down transition.
- **Docker mapping** — agents report their container & Swarm service inventory, so creating
  a docker monitor is: pick agent → pick container/service from a dropdown → give it a
  friendly name. Done.
- **Admin auth via GitHub OAuth**, allow-listed by GitHub username.
- Everything except OAuth is configured in the admin web UI.

## Quick start

### 1. Create a GitHub OAuth app

GitHub → Settings → Developer settings → OAuth Apps → New.
Callback URL: `https://your-server/auth/callback`.

### 2. Run the server

```bash
cp .env.example .env   # fill in OAuth credentials + your GitHub username
docker compose up -d server
```

Or without Docker:

```bash
go build ./cmd/tsundere-server
TSUNDERE_BASE_URL=https://status.example.com \
TSUNDERE_GITHUB_CLIENT_ID=… \
TSUNDERE_GITHUB_CLIENT_SECRET=… \
TSUNDERE_ADMIN_USERS=4G0NYY \
./tsundere-server
```

Put it behind your reverse proxy of choice for TLS. It listens on `:8420` by default.

### 3. Deploy agents

In the admin UI: **Agents → + New agent**. Copy the token (shown once!), then on each host:

```bash
tsundere-agent -server https://status.example.com -token tsa_…
```

or via Docker (gives the agent access to the host's Docker daemon):

```bash
docker run -d --restart unless-stopped --name tsundere-agent \
  -e TSUNDERE_SERVER_URL=https://status.example.com \
  -e TSUNDERE_AGENT_TOKEN=tsa_… \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  ghcr.io/4g0nyy/tsundere-agent:latest
```

Images for the agent and server are built automatically for amd64/arm64 on every push
to `main` (see `.github/workflows/docker.yml`). The images contain nothing but the
compiled binary — tokens and settings are passed in at runtime, so they're safe to
keep public.

The agent phones home every 30 seconds for its monitor list, so new/edited monitors are
picked up automatically. If an agent stops reporting, its monitors are marked **down**
after ~2 missed intervals and you get alerted.

## Configuration reference

### Server (environment)

| Variable | Default | Purpose |
|---|---|---|
| `TSUNDERE_LISTEN` | `:8420` | listen address |
| `TSUNDERE_DB` | `tsundere.db` | SQLite file path |
| `TSUNDERE_BASE_URL` | `http://localhost:8420` | public URL (OAuth callback, cookie security) |
| `TSUNDERE_GITHUB_CLIENT_ID` | — | GitHub OAuth app client ID |
| `TSUNDERE_GITHUB_CLIENT_SECRET` | — | GitHub OAuth app client secret |
| `TSUNDERE_ADMIN_USERS` | — | comma-separated GitHub usernames allowed as admins |
| `TSUNDERE_DEV_NO_AUTH` | — | set to `1` to disable auth. **Local development only.** |

Everything else — site title, Discord webhook, SMTP host/port/credentials/recipients —
lives in **Settings** in the admin UI, with test buttons for both alert channels.

### Agent (flags or environment)

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `-server` | `TSUNDERE_SERVER_URL` | — | server base URL |
| `-token` | `TSUNDERE_AGENT_TOKEN` | — | agent token from the admin UI |
| `-docker` | `TSUNDERE_DOCKER_HOST` | `/var/run/docker.sock` | docker socket path or `tcp://host:2375`; `off` disables docker checks |

## Architecture

```
┌────────────┐   GET /api/agent/config (Bearer token)   ┌──────────────────┐
│   agent    │ ───────────────────────────────────────► │      server      │
│ (per host) │ ◄─────────────────────────────────────── │  SQLite + web UI │
│ docker.sock│   POST /api/agent/results, /inventory    │  alerts, engine  │
└────────────┘                                          └──────────────────┘
                                                           │          │
                                                    /admin (OAuth)  /status/<slug>
```

Heartbeats are retained for 90 days and pruned automatically.

## Development

```bash
TSUNDERE_DEV_NO_AUTH=1 go run ./cmd/tsundere-server
# admin UI at http://localhost:8420/admin — no login required in dev mode
```

---

*It's not like this README was written for you specifically or anything.*
