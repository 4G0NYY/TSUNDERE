/* TSUNDERE admin UI — a small hash-routed SPA, no framework needed. */
"use strict";

const app = document.getElementById("app");
let ME = null;

const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
}[c]));

const fmtTime = ts => ts ? new Date(ts * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "–";
const fmtAgo = ts => {
  if (!ts) return "never";
  const s = Math.floor(Date.now() / 1000 - ts);
  if (s < 5) return "just now";
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
};
// unix seconds <-> <input type=datetime-local> in local time
const toLocalInput = ts => {
  const d = new Date((ts || Date.now() / 1000) * 1000);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
};
const fromLocalInput = v => v ? Math.floor(new Date(v).getTime() / 1000) : 0;

// ---------- api / toast / modal ----------

async function api(path, opts = {}) {
  if (opts.body !== undefined && typeof opts.body !== "string") {
    opts.body = JSON.stringify(opts.body);
    opts.headers = { "Content-Type": "application/json", ...opts.headers };
  }
  const resp = await fetch(path, opts);
  if (resp.status === 401) { renderLogin(); throw new Error("not logged in"); }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || ("HTTP " + resp.status));
  return data;
}

function toast(msg, isError = false) {
  const t = document.createElement("div");
  t.className = "toast" + (isError ? " error" : "");
  t.textContent = msg;
  document.body.appendChild(t);
  setTimeout(() => t.remove(), isError ? 6000 : 3000);
}
const oops = e => toast("💢 " + e.message, true);

function modal(html) {
  closeModal();
  const back = document.createElement("div");
  back.className = "modal-backdrop";
  back.id = "modal";
  back.innerHTML = `<div class="modal">${html}</div>`;
  back.addEventListener("mousedown", e => { if (e.target === back) closeModal(); });
  document.body.appendChild(back);
  return back;
}
function closeModal() { document.getElementById("modal")?.remove(); }
document.addEventListener("keydown", e => { if (e.key === "Escape") closeModal(); });

// ---------- shell / routing ----------

const ROUTES = {
  dashboard:   { label: "Dashboard",    render: viewDashboard },
  monitors:    { label: "Monitors",     render: viewMonitors },
  agents:      { label: "Agents",       render: viewAgents },
  pages:       { label: "Status pages", render: viewPages },
  incidents:   { label: "Incidents",    render: viewIncidents },
  maintenance: { label: "Maintenance",  render: viewMaintenance },
  settings:    { label: "Settings",     render: viewSettings },
};

function renderLogin() {
  ME = null;
  app.innerHTML = `
    <div class="login-wrap">
      <div class="card login-card">
        <div class="logo">TSUNDERE</div>
        <p class="muted" style="font-size:.78rem">Telemetry, System Uptime, &amp; Network Data Engine for Rapid Evaluation</p>
        <p>W-wait, who are you?! This is the admin area.<br>Prove you belong here… n-not that I care.</p>
        <a class="btn primary" href="/auth/login" style="display:inline-block;margin-top:10px">Sign in with GitHub</a>
      </div>
    </div>`;
}

function shell(active, contentHTML) {
  const nav = Object.entries(ROUTES).map(([key, r]) =>
    `<a href="#${key}" class="${key === active ? "active" : ""}">${r.label}</a>`).join("");
  app.innerHTML = `
    <div class="app">
      <aside class="sidebar">
        <div class="logo">TSUNDERE<small>Telemetry, System Uptime, &amp; Network<br>Data Engine for Rapid Evaluation</small></div>
        <nav>${nav}</nav>
        <div class="whoami">logged in as <b>${esc(ME?.login)}</b> · <a href="/auth/logout">logout</a></div>
      </aside>
      <main class="main" id="main">${contentHTML}</main>
    </div>`;
  return document.getElementById("main");
}

async function route() {
  if (!ME) {
    try { ME = await api("/api/admin/me"); } catch { return; }
  }
  const key = location.hash.replace("#", "") || "dashboard";
  const r = ROUTES[key] || ROUTES.dashboard;
  try { await r.render(key); } catch (e) { if (e.message !== "not logged in") oops(e); }
}
window.addEventListener("hashchange", route);

// ---------- dashboard ----------

async function viewDashboard() {
  const [ov, monitors] = await Promise.all([api("/api/admin/overview"), api("/api/admin/monitors")]);
  const line = ov.monitors_down > 0
    ? "H-hey! Something is down! Go fix it, don't just stare at me!"
    : (ov.monitors_up > 0 ? "Everything you told me to watch is up. Hmph. You're welcome, I guess."
                          : "Nothing to monitor yet. It's not like I'm bored or anything…");
  const main = shell("dashboard", `
    <h1>Dashboard</h1>
    <p class="muted">${esc(line)}</p>
    <div class="tiles">
      <div class="tile up"><div class="num">${ov.monitors_up}</div><div class="lbl">up</div></div>
      <div class="tile down"><div class="num">${ov.monitors_down}</div><div class="lbl">down</div></div>
      <div class="tile pending"><div class="num">${ov.monitors_pending}</div><div class="lbl">pending</div></div>
      <div class="tile accent"><div class="num">${ov.agents_online}/${ov.agents_total}</div><div class="lbl">agents online</div></div>
    </div>
    <div class="card">
      <h2>Monitors</h2>
      ${monitorTable(monitors, false)}
    </div>`);
  main.querySelectorAll("[data-goto]").forEach(el =>
    el.addEventListener("click", () => location.hash = el.dataset.goto));
}

const STATUS_BADGE = {
  0: '<span class="badge down">down</span>',
  1: '<span class="badge up">up</span>',
  2: '<span class="badge pending">pending</span>',
};

function monitorTable(monitors, withActions) {
  if (!monitors.length) return '<p class="empty">No monitors yet. Go on, create one already!</p>';
  return `<table><thead><tr>
      <th>Status</th><th>Name</th><th>Type</th><th>Agent</th><th>Interval</th>${withActions ? "<th></th>" : ""}
    </tr></thead><tbody>` +
    monitors.map(m => `<tr>
      <td>${m.enabled ? (STATUS_BADGE[m.last_status] || "") : '<span class="badge pending">paused</span>'}</td>
      <td><b>${esc(m.name)}</b><br><span class="muted mono" style="font-size:.75rem">${esc(monitorTargetLabel(m))}</span></td>
      <td>${esc(m.type)}</td>
      <td>${esc(m.agent_name || m.agent_id)}${m.agent_hostname ? `<br><span class="muted mono" style="font-size:.75rem">${esc(m.agent_hostname)}</span>` : ""}</td>
      <td>${m.interval_sec}s</td>
      ${withActions ? `<td style="text-align:right;white-space:nowrap">
        <button class="small" data-edit="${m.id}">edit</button>
        <button class="small danger" data-del="${m.id}">delete</button></td>` : ""}
    </tr>`).join("") + "</tbody></table>";
}

function monitorTargetLabel(m) {
  const c = m.config || {};
  switch (m.type) {
    case "ping": return c.host || "";
    case "dns": return `${c.query || "?"} @ ${c.server || "?"}`;
    case "https": return c.url || "";
    case "docker": return `${c.target_type || "container"}: ${c.target || "?"}`;
  }
  return "";
}

// ---------- monitors ----------

async function viewMonitors() {
  const [monitors, agents] = await Promise.all([api("/api/admin/monitors"), api("/api/admin/agents")]);
  monitors.forEach(m => { try { m.config = JSON.parse(m.config); } catch {} });
  const main = shell("monitors", `
    <div class="row" style="justify-content:space-between;align-items:center">
      <h1>Monitors</h1>
      <button class="primary" id="new-monitor">+ New monitor</button>
    </div>
    <div class="card">${monitorTable(monitors, true)}</div>`);

  main.querySelector("#new-monitor").addEventListener("click", () => monitorForm(null, agents));
  main.querySelectorAll("[data-edit]").forEach(b => b.addEventListener("click", () => {
    const m = monitors.find(x => x.id == b.dataset.edit);
    monitorForm(m, agents);
  }));
  main.querySelectorAll("[data-del]").forEach(b => b.addEventListener("click", async () => {
    const m = monitors.find(x => x.id == b.dataset.del);
    if (!confirm(`Delete monitor "${m.name}" and all its history? This can't be undone, you know.`)) return;
    try { await api("/api/admin/monitors/" + m.id, { method: "DELETE" }); toast("Deleted. Happy now?"); route(); }
    catch (e) { oops(e); }
  }));
}

function monitorForm(m, agents) {
  if (!agents.length) { toast("Create an agent first — someone has to actually run the checks, baka.", true); location.hash = "agents"; return; }
  const isNew = !m;
  m = m || { type: "https", interval_sec: 60, enabled: true, config: {}, agent_id: agents[0].id };
  const c = m.config || {};

  const agentOpts = agents.map(a =>
    `<option value="${a.id}" ${a.id === m.agent_id ? "selected" : ""}>${esc(a.name)}${a.online ? "" : " (offline)"}</option>`).join("");

  modal(`
    <h2>${isNew ? "New monitor" : "Edit monitor"}</h2>
    <label>Friendly name</label>
    <input id="mf-name" value="${esc(m.name || "")}" placeholder="e.g. Main website">
    <label>Type</label>
    <select id="mf-type">
      ${["https", "ping", "dns", "docker"].map(t => `<option ${t === m.type ? "selected" : ""}>${t}</option>`).join("")}
    </select>
    <label>Agent (runs the check)</label>
    <select id="mf-agent">${agentOpts}</select>
    <label>Check interval (seconds)</label>
    <input id="mf-interval" type="number" min="10" value="${m.interval_sec}">
    <div id="mf-typefields"></div>
    <div class="checkline"><input type="checkbox" id="mf-enabled" ${m.enabled ? "checked" : ""}><span>Enabled</span></div>
    <div class="actions">
      <button onclick="closeModal()">Cancel</button>
      <button class="primary" id="mf-save">${isNew ? "Create" : "Save"}</button>
    </div>`);

  const typeFields = document.getElementById("mf-typefields");
  const agentSel = document.getElementById("mf-agent");
  const typeSel = document.getElementById("mf-type");

  function renderTypeFields() {
    const t = typeSel.value;
    if (t === "https") {
      typeFields.innerHTML = `
        <label>URL</label><input id="cf-url" value="${esc(c.url || "")}" placeholder="https://example.com">
        <label>Keyword the response must contain (optional)</label><input id="cf-keyword" value="${esc(c.keyword || "")}">
        <label>Timeout (seconds)</label><input id="cf-timeout" type="number" value="${c.timeout_sec || 10}">
        <div class="checkline"><input type="checkbox" id="cf-skiptls" ${c.skip_tls_verify ? "checked" : ""}><span>Skip TLS verification (self-signed certs)</span></div>`;
    } else if (t === "ping") {
      typeFields.innerHTML = `
        <label>Host / IP</label><input id="cf-host" value="${esc(c.host || "")}" placeholder="10.0.0.1 or host.example.com">
        <label>Timeout (seconds)</label><input id="cf-timeout" type="number" value="${c.timeout_sec || 5}">`;
    } else if (t === "dns") {
      typeFields.innerHTML = `
        <label>DNS server to test</label><input id="cf-server" value="${esc(c.server || "")}" placeholder="1.1.1.1">
        <label>Name to resolve</label><input id="cf-query" value="${esc(c.query || "")}" placeholder="example.com">
        <label>Record type</label>
        <select id="cf-rtype">${["A", "AAAA", "CNAME", "MX", "NS", "TXT"].map(r =>
          `<option ${r === (c.record_type || "A") ? "selected" : ""}>${r}</option>`).join("")}</select>
        <label>Answer must contain (optional)</label><input id="cf-expected" value="${esc(c.expected || "")}">`;
    } else if (t === "docker") {
      const agent = agents.find(a => a.id == agentSel.value) || {};
      let inv = {};
      try { inv = typeof agent.inventory === "string" ? JSON.parse(agent.inventory) : (agent.inventory || {}); } catch {}
      const targetType = c.target_type || "container";
      const list = (targetType === "service" ? inv.services : inv.containers) || [];
      const opts = list.map(n => `<option ${n === c.target ? "selected" : ""}>${esc(n)}</option>`).join("");
      typeFields.innerHTML = `
        <label>Map to</label>
        <select id="cf-ttype">
          <option value="container" ${targetType === "container" ? "selected" : ""}>Docker container</option>
          <option value="service" ${targetType === "service" ? "selected" : ""}>Swarm service</option>
        </select>
        <label>Target ${list.length ? "(reported by this agent)" : ""}</label>
        ${list.length
          ? `<select id="cf-target">${opts}${c.target && !list.includes(c.target) ? `<option selected>${esc(c.target)}</option>` : ""}</select>`
          : `<input id="cf-target" value="${esc(c.target || "")}" placeholder="container or service name">
             <p class="muted" style="font-size:.78rem;margin:4px 0 0">This agent hasn't reported a docker inventory yet — type the name manually, or wait a minute for it to phone home.</p>`}`;
      typeFields.querySelector("#cf-ttype").addEventListener("change", () => {
        c.target_type = typeFields.querySelector("#cf-ttype").value;
        renderTypeFields();
      });
    }
  }
  renderTypeFields();
  typeSel.addEventListener("change", renderTypeFields);
  agentSel.addEventListener("change", () => { if (typeSel.value === "docker") renderTypeFields(); });

  document.getElementById("mf-save").addEventListener("click", async () => {
    const t = typeSel.value;
    const g = id => document.getElementById(id)?.value?.trim() ?? "";
    let config = {};
    if (t === "https") config = { url: g("cf-url"), keyword: g("cf-keyword"), timeout_sec: +g("cf-timeout") || 10, skip_tls_verify: document.getElementById("cf-skiptls").checked };
    if (t === "ping") config = { host: g("cf-host"), timeout_sec: +g("cf-timeout") || 5 };
    if (t === "dns") config = { server: g("cf-server"), query: g("cf-query"), record_type: g("cf-rtype"), expected: g("cf-expected") };
    if (t === "docker") config = { target_type: g("cf-ttype"), target: g("cf-target") };

    const body = {
      name: g("mf-name"), type: t, agent_id: +agentSel.value,
      interval_sec: +g("mf-interval") || 60,
      enabled: document.getElementById("mf-enabled").checked,
      config: config,
    };
    try {
      if (isNew) await api("/api/admin/monitors", { method: "POST", body });
      else await api("/api/admin/monitors/" + m.id, { method: "PUT", body });
      closeModal(); toast(isNew ? "Monitor created. I'll keep an eye on it… since you asked." : "Saved."); route();
    } catch (e) { oops(e); }
  });
}

// ---------- agents ----------

async function viewAgents() {
  const agents = await api("/api/admin/agents");
  const rows = agents.map(a => {
    let inv = {};
    try { inv = typeof a.inventory === "string" ? JSON.parse(a.inventory) : (a.inventory || {}); } catch {}
    const nC = (inv.containers || []).length, nS = (inv.services || []).length;
    return `<tr>
      <td>${a.online ? '<span class="badge up">online</span>' : '<span class="badge down">offline</span>'}</td>
      <td><b>${esc(a.name)}</b></td>
      <td class="muted mono" style="font-size:.8rem">${esc(a.hostname) || "–"}</td>
      <td class="muted">${fmtAgo(a.last_seen)}</td>
      <td class="muted">${nC || nS ? `${nC} containers · ${nS} services` : "–"}</td>
      <td style="text-align:right;white-space:nowrap">
        <button class="small" data-rename="${a.id}">rename</button>
        <button class="small" data-token="${a.id}">new token</button>
        <button class="small danger" data-del="${a.id}">delete</button>
      </td></tr>`;
  }).join("");

  const main = shell("agents", `
    <div class="row" style="justify-content:space-between;align-items:center">
      <h1>Agents</h1>
      <button class="primary" id="new-agent">+ New agent</button>
    </div>
    <div class="card">
      ${agents.length ? `<table><thead><tr><th>Status</th><th>Name</th><th>Hostname</th><th>Last seen</th><th>Docker inventory</th><th></th></tr></thead><tbody>${rows}</tbody></table>`
                      : '<p class="empty">No agents yet. Deploy one on each host you want monitored — I can\'t be everywhere at once, you know!</p>'}
    </div>`);

  main.querySelector("#new-agent").addEventListener("click", () => {
    modal(`
      <h2>New agent</h2>
      <label>Friendly name (e.g. the hostname)</label>
      <input id="ag-name" placeholder="vps-1">
      <div class="actions">
        <button onclick="closeModal()">Cancel</button>
        <button class="primary" id="ag-create">Create</button>
      </div>`);
    document.getElementById("ag-create").addEventListener("click", async () => {
      const name = document.getElementById("ag-name").value.trim();
      if (!name) return toast("It needs a name!", true);
      try {
        const res = await api("/api/admin/agents", { method: "POST", body: { name } });
        showToken(res.agent.name, res.token);
      } catch (e) { oops(e); }
    });
  });

  main.querySelectorAll("[data-rename]").forEach(b => b.addEventListener("click", async () => {
    const a = agents.find(x => x.id == b.dataset.rename);
    const name = prompt("New name for this agent:", a.name);
    if (!name) return;
    try { await api("/api/admin/agents/" + a.id, { method: "PUT", body: { name } }); route(); } catch (e) { oops(e); }
  }));
  main.querySelectorAll("[data-token]").forEach(b => b.addEventListener("click", async () => {
    const a = agents.find(x => x.id == b.dataset.token);
    if (!confirm(`Generate a new token for "${a.name}"? The old one stops working immediately.`)) return;
    try {
      const res = await api(`/api/admin/agents/${a.id}/regenerate-token`, { method: "POST" });
      showToken(a.name, res.token);
    } catch (e) { oops(e); }
  }));
  main.querySelectorAll("[data-del]").forEach(b => b.addEventListener("click", async () => {
    const a = agents.find(x => x.id == b.dataset.del);
    if (!confirm(`Delete agent "${a.name}"? All its monitors and their history go with it!`)) return;
    try { await api("/api/admin/agents/" + a.id, { method: "DELETE" }); toast("Gone."); route(); } catch (e) { oops(e); }
  }));
}

function showToken(name, token) {
  const cmd = `tsundere-agent -server ${location.origin} -token ${token}`;
  modal(`
    <h2>Agent token for "${esc(name)}"</h2>
    <p>Copy this <b>now</b> — it is shown exactly once. I-it's not like I'll remember it for you!</p>
    <div class="token-box" id="tok">${esc(token)}</div>
    <p class="muted" style="font-size:.82rem">Run the agent like this:</p>
    <div class="token-box" style="border-style:solid">${esc(cmd)}</div>
    <div class="actions">
      <button id="tok-copy">Copy token</button>
      <button class="primary" onclick="closeModal();route()">Done</button>
    </div>`);
  document.getElementById("tok-copy").addEventListener("click", () => {
    navigator.clipboard.writeText(token).then(() => toast("Copied. Don't lose it."));
  });
}

// ---------- status pages ----------

async function viewPages() {
  const [pages, monitors] = await Promise.all([api("/api/admin/status-pages"), api("/api/admin/monitors")]);
  const rows = pages.map(p => `<tr>
    <td><b>${esc(p.title)}</b></td>
    <td class="mono"><a href="/status/${esc(p.slug)}" target="_blank">/status/${esc(p.slug)}</a></td>
    <td>${p.published ? '<span class="badge up">published</span>' : '<span class="badge pending">draft</span>'}</td>
    <td class="muted">${p.monitor_ids.length} monitors</td>
    <td style="text-align:right;white-space:nowrap">
      <button class="small" data-edit="${p.id}">edit</button>
      <button class="small danger" data-del="${p.id}">delete</button>
    </td></tr>`).join("");

  const main = shell("pages", `
    <div class="row" style="justify-content:space-between;align-items:center">
      <h1>Status pages</h1>
      <button class="primary" id="new-page">+ New page</button>
    </div>
    <div class="card">
      ${pages.length ? `<table><thead><tr><th>Title</th><th>URL</th><th>State</th><th>Monitors</th><th></th></tr></thead><tbody>${rows}</tbody></table>`
                     : '<p class="empty">No status pages yet. Your users deserve to know what\'s up… literally.</p>'}
    </div>`);

  const form = (p) => {
    const isNew = !p;
    p = p || { published: true, monitor_ids: [] };
    const checks = monitors.map(m => `
      <div class="checkline"><input type="checkbox" class="pg-mon" value="${m.id}" ${p.monitor_ids.includes(m.id) ? "checked" : ""}>
      <span>${esc(m.name)} <span class="muted">(${esc(m.type)}${m.agent_hostname ? " · " + esc(m.agent_hostname) : ""})</span></span></div>`).join("");
    modal(`
      <h2>${isNew ? "New status page" : "Edit status page"}</h2>
      <label>Title</label><input id="pg-title" value="${esc(p.title || "")}" placeholder="My Services">
      <label>Slug (URL: /status/&lt;slug&gt;)</label><input id="pg-slug" value="${esc(p.slug || "")}" placeholder="main">
      <label>Description (optional)</label><textarea id="pg-desc" rows="2">${esc(p.description || "")}</textarea>
      <div class="row" style="justify-content:space-between;align-items:center">
        <label style="margin-bottom:0">Monitors shown on this page</label>
        ${monitors.length ? '<button class="small" id="pg-all" type="button" style="margin-top:12px">display all</button>' : ""}
      </div>
      ${checks || '<p class="muted">No monitors exist yet.</p>'}
      <div class="checkline"><input type="checkbox" id="pg-hosts" ${p.show_hostnames ? "checked" : ""}><span>Display host name (agent hostname under each monitor)</span></div>
      <div class="checkline"><input type="checkbox" id="pg-pub" ${p.published ? "checked" : ""}><span>Published (publicly visible)</span></div>
      <div class="actions">
        <button onclick="closeModal()">Cancel</button>
        <button class="primary" id="pg-save">${isNew ? "Create" : "Save"}</button>
      </div>`);
    // "display all" checks every monitor; if everything is checked already, it clears instead.
    document.getElementById("pg-all")?.addEventListener("click", () => {
      const boxes = [...document.querySelectorAll(".pg-mon")];
      const allChecked = boxes.every(b => b.checked);
      boxes.forEach(b => b.checked = !allChecked);
    });
    document.getElementById("pg-save").addEventListener("click", async () => {
      const body = {
        title: document.getElementById("pg-title").value,
        slug: document.getElementById("pg-slug").value,
        description: document.getElementById("pg-desc").value,
        published: document.getElementById("pg-pub").checked,
        show_hostnames: document.getElementById("pg-hosts").checked,
        monitor_ids: [...document.querySelectorAll(".pg-mon:checked")].map(c => +c.value),
      };
      try {
        if (isNew) await api("/api/admin/status-pages", { method: "POST", body });
        else await api("/api/admin/status-pages/" + p.id, { method: "PUT", body });
        closeModal(); toast("Saved. Go look at it, I did a good job. Obviously."); route();
      } catch (e) { oops(e); }
    });
  };

  main.querySelector("#new-page").addEventListener("click", () => form(null));
  main.querySelectorAll("[data-edit]").forEach(b => b.addEventListener("click", () =>
    form(pages.find(x => x.id == b.dataset.edit))));
  main.querySelectorAll("[data-del]").forEach(b => b.addEventListener("click", async () => {
    const p = pages.find(x => x.id == b.dataset.del);
    if (!confirm(`Delete status page "${p.title}" (and its incidents)?`)) return;
    try { await api("/api/admin/status-pages/" + p.id, { method: "DELETE" }); toast("Deleted."); route(); } catch (e) { oops(e); }
  }));
}

// ---------- incidents ----------

async function viewIncidents() {
  const [incidents, pages] = await Promise.all([api("/api/admin/incidents"), api("/api/admin/status-pages")]);
  const pageName = id => pages.find(p => p.id === id)?.title || ("page #" + id);
  const rows = incidents.map(i => `<tr>
    <td>${i.resolved_at ? '<span class="badge up">resolved</span>' : `<span class="badge down">${esc(i.severity)}</span>`}</td>
    <td><b>${esc(i.title)}</b><br><span class="muted" style="font-size:.8rem">${esc(pageName(i.page_id))}</span></td>
    <td class="muted">${fmtTime(i.created_at)}</td>
    <td style="text-align:right;white-space:nowrap">
      ${i.resolved_at ? "" : `<button class="small" data-resolve="${i.id}">resolve</button>`}
      <button class="small" data-edit="${i.id}">edit</button>
      <button class="small danger" data-del="${i.id}">delete</button>
    </td></tr>`).join("");

  const main = shell("incidents", `
    <div class="row" style="justify-content:space-between;align-items:center">
      <h1>Incidents</h1>
      <button class="primary" id="new-inc">+ Publish incident</button>
    </div>
    <div class="card">
      ${incidents.length ? `<table><thead><tr><th>State</th><th>Incident</th><th>Created</th><th></th></tr></thead><tbody>${rows}</tbody></table>`
                         : '<p class="empty">No incidents. Which is… good?! Why would you want incidents?!</p>'}
    </div>`);

  const form = (i) => {
    if (!pages.length) { toast("Create a status page first — incidents are published onto one.", true); location.hash = "pages"; return; }
    const isNew = !i;
    i = i || { severity: "minor", page_id: pages[0].id };
    modal(`
      <h2>${isNew ? "Publish incident" : "Edit incident"}</h2>
      <label>Status page</label>
      <select id="in-page" ${isNew ? "" : "disabled"}>${pages.map(p =>
        `<option value="${p.id}" ${p.id === i.page_id ? "selected" : ""}>${esc(p.title)}</option>`).join("")}</select>
      <label>Title</label><input id="in-title" value="${esc(i.title || "")}" placeholder="Database degraded">
      <label>Details (shown publicly)</label><textarea id="in-body" rows="4">${esc(i.body || "")}</textarea>
      <label>Severity</label>
      <select id="in-sev">${["info", "minor", "major", "critical"].map(s =>
        `<option ${s === i.severity ? "selected" : ""}>${s}</option>`).join("")}</select>
      <div class="actions">
        <button onclick="closeModal()">Cancel</button>
        <button class="primary" id="in-save">${isNew ? "Publish" : "Save"}</button>
      </div>`);
    document.getElementById("in-save").addEventListener("click", async () => {
      const body = {
        page_id: +document.getElementById("in-page").value,
        title: document.getElementById("in-title").value,
        body: document.getElementById("in-body").value,
        severity: document.getElementById("in-sev").value,
      };
      try {
        if (isNew) await api("/api/admin/incidents", { method: "POST", body });
        else await api("/api/admin/incidents/" + i.id, { method: "PUT", body });
        closeModal(); toast(isNew ? "Incident published. Now go actually fix it!" : "Saved."); route();
      } catch (e) { oops(e); }
    });
  };

  main.querySelector("#new-inc").addEventListener("click", () => form(null));
  main.querySelectorAll("[data-edit]").forEach(b => b.addEventListener("click", () =>
    form(incidents.find(x => x.id == b.dataset.edit))));
  main.querySelectorAll("[data-resolve]").forEach(b => b.addEventListener("click", async () => {
    try { await api(`/api/admin/incidents/${b.dataset.resolve}/resolve`, { method: "POST" }); toast("Resolved. See? Everything worked out. I told you."); route(); }
    catch (e) { oops(e); }
  }));
  main.querySelectorAll("[data-del]").forEach(b => b.addEventListener("click", async () => {
    if (!confirm("Delete this incident?")) return;
    try { await api("/api/admin/incidents/" + b.dataset.del, { method: "DELETE" }); route(); } catch (e) { oops(e); }
  }));
}

// ---------- maintenance ----------

async function viewMaintenance() {
  const [maints, monitors] = await Promise.all([api("/api/admin/maintenances"), api("/api/admin/monitors")]);
  const nowS = Date.now() / 1000;
  const state = m => nowS < m.start_at ? '<span class="badge pending">scheduled</span>'
    : nowS <= m.end_at ? '<span class="badge maint">active</span>' : '<span class="badge accent">past</span>';
  const monName = id => monitors.find(m => m.id === id)?.name || ("#" + id);

  const rows = maints.map(m => `<tr>
    <td>${state(m)}</td>
    <td><b>${esc(m.title)}</b><br><span class="muted" style="font-size:.8rem">${m.monitor_ids.map(id => esc(monName(id))).join(", ") || "no monitors selected"}</span></td>
    <td class="muted">${fmtTime(m.start_at)}<br>→ ${fmtTime(m.end_at)}</td>
    <td style="text-align:right;white-space:nowrap">
      <button class="small" data-edit="${m.id}">edit</button>
      <button class="small danger" data-del="${m.id}">delete</button>
    </td></tr>`).join("");

  const main = shell("maintenance", `
    <div class="row" style="justify-content:space-between;align-items:center">
      <h1>Maintenance</h1>
      <button class="primary" id="new-mt">+ Schedule maintenance</button>
    </div>
    <p class="muted">Alerts for the selected monitors are paused while a window is active, and the window is announced on any status page showing those monitors.</p>
    <div class="card">
      ${maints.length ? `<table><thead><tr><th>State</th><th>Maintenance</th><th>Window</th><th></th></tr></thead><tbody>${rows}</tbody></table>`
                      : '<p class="empty">Nothing scheduled. Planning ahead? How responsible of you. F-for once.</p>'}
    </div>`);

  const form = (m) => {
    const isNew = !m;
    m = m || { start_at: Math.floor(nowS + 3600), end_at: Math.floor(nowS + 7200), monitor_ids: [] };
    const checks = monitors.map(mon => `
      <div class="checkline"><input type="checkbox" class="mt-mon" value="${mon.id}" ${m.monitor_ids.includes(mon.id) ? "checked" : ""}>
      <span>${esc(mon.name)} <span class="muted">(${esc(mon.type)}${mon.agent_hostname ? " · " + esc(mon.agent_hostname) : ""})</span></span></div>`).join("");
    modal(`
      <h2>${isNew ? "Schedule maintenance" : "Edit maintenance"}</h2>
      <label>Title</label><input id="mt-title" value="${esc(m.title || "")}" placeholder="Kernel upgrade on vps-1">
      <label>Details (optional, shown publicly)</label><textarea id="mt-body" rows="3">${esc(m.body || "")}</textarea>
      <label>Start</label><input id="mt-start" type="datetime-local" value="${toLocalInput(m.start_at)}">
      <label>End</label><input id="mt-end" type="datetime-local" value="${toLocalInput(m.end_at)}">
      <label>Affected monitors (alerts paused)</label>
      ${checks || '<p class="muted">No monitors exist yet.</p>'}
      <div class="actions">
        <button onclick="closeModal()">Cancel</button>
        <button class="primary" id="mt-save">${isNew ? "Schedule" : "Save"}</button>
      </div>`);
    document.getElementById("mt-save").addEventListener("click", async () => {
      const body = {
        title: document.getElementById("mt-title").value,
        body: document.getElementById("mt-body").value,
        start_at: fromLocalInput(document.getElementById("mt-start").value),
        end_at: fromLocalInput(document.getElementById("mt-end").value),
        monitor_ids: [...document.querySelectorAll(".mt-mon:checked")].map(c => +c.value),
      };
      try {
        if (isNew) await api("/api/admin/maintenances", { method: "POST", body });
        else await api("/api/admin/maintenances/" + m.id, { method: "PUT", body });
        closeModal(); toast("Scheduled. I'll keep quiet about those monitors. You owe me."); route();
      } catch (e) { oops(e); }
    });
  };

  main.querySelector("#new-mt").addEventListener("click", () => form(null));
  main.querySelectorAll("[data-edit]").forEach(b => b.addEventListener("click", () =>
    form(maints.find(x => x.id == b.dataset.edit))));
  main.querySelectorAll("[data-del]").forEach(b => b.addEventListener("click", async () => {
    if (!confirm("Delete this maintenance window?")) return;
    try { await api("/api/admin/maintenances/" + b.dataset.del, { method: "DELETE" }); route(); } catch (e) { oops(e); }
  }));
}

// ---------- settings ----------

async function viewSettings() {
  const s = await api("/api/admin/settings");
  const main = shell("settings", `
    <h1>Settings</h1>
    <div class="card">
      <h2>General</h2>
      <label>Site title</label>
      <input id="st-site_title" value="${esc(s.site_title)}" placeholder="TSUNDERE">
    </div>
    <div class="card">
      <h2>Discord alerts</h2>
      <label>Webhook URL</label>
      <input id="st-discord_webhook_url" value="${esc(s.discord_webhook_url)}" placeholder="https://discord.com/api/webhooks/…">
      <div style="margin-top:10px"><button id="test-discord">Send test alert</button></div>
    </div>
    <div class="card">
      <h2>Mail alerts (SMTP)</h2>
      <div class="row">
        <div style="flex:3;min-width:180px"><label>Host</label><input id="st-smtp_host" value="${esc(s.smtp_host)}" placeholder="smtp.example.com"></div>
        <div style="flex:1;min-width:90px"><label>Port</label><input id="st-smtp_port" value="${esc(s.smtp_port)}" placeholder="587"></div>
      </div>
      <div class="row">
        <div style="flex:1;min-width:180px"><label>Username</label><input id="st-smtp_user" value="${esc(s.smtp_user)}"></div>
        <div style="flex:1;min-width:180px"><label>Password</label><input id="st-smtp_pass" type="password" value="${esc(s.smtp_pass)}"></div>
      </div>
      <label>From address</label><input id="st-smtp_from" value="${esc(s.smtp_from)}" placeholder="tsundere@example.com">
      <label>Recipients (comma separated)</label><input id="st-smtp_to" value="${esc(s.smtp_to)}" placeholder="you@example.com, oncall@example.com">
      <div style="margin-top:10px"><button id="test-email">Send test mail</button></div>
    </div>
    <button class="primary" id="save-settings">Save settings</button>
    <p class="muted" style="font-size:.8rem;margin-top:14px">GitHub OAuth (client ID/secret, admin list) is configured via environment variables on the server — that's the one thing I won't let you change from here. It's for your own good.</p>`);

  main.querySelector("#save-settings").addEventListener("click", async () => {
    const body = {};
    for (const k of ["site_title", "discord_webhook_url", "smtp_host", "smtp_port", "smtp_user", "smtp_pass", "smtp_from", "smtp_to"]) {
      body[k] = document.getElementById("st-" + k).value;
    }
    try { await api("/api/admin/settings", { method: "PUT", body }); toast("Settings saved. You're welcome."); }
    catch (e) { oops(e); }
  });
  main.querySelector("#test-discord").addEventListener("click", async () => {
    try { await api("/api/admin/settings/test-discord", { method: "POST" }); toast("Test sent — go check Discord."); }
    catch (e) { oops(e); }
  });
  main.querySelector("#test-email").addEventListener("click", async () => {
    try { await api("/api/admin/settings/test-email", { method: "POST" }); toast("Test mail sent — go check your inbox."); }
    catch (e) { oops(e); }
  });
}

// expose for inline onclick handlers
window.closeModal = closeModal;
window.route = route;

route();
