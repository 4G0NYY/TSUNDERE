/* TSUNDERE public status page */
"use strict";

const slug = decodeURIComponent(location.pathname.split("/").pop());
const app = document.getElementById("app");

const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
}[c]));

const OVERALL = {
  up:          { cls: "up",          dot: "up",    text: "All systems operational.",
                 sub: "Everything is running. Obviously. It's not like anyone worked hard for this or anything." },
  degraded:    { cls: "degraded",    dot: "down",  text: "Some systems are acting up.",
                 sub: "I-it's being handled, okay?! Check the incident notes below." },
  down:        { cls: "down",        dot: "down",  text: "S-some services are down!",
                 sub: "Don't look at me like that — it's being worked on." },
  maintenance: { cls: "maintenance", dot: "maint", text: "Maintenance in progress.",
                 sub: "Quit whining, it'll be back soon." },
};

const STATUS_DOT = { 0: "down", 1: "up", 2: "pending", 3: "maint" };
const STATUS_LABEL = { 0: "Down", 1: "Up", 2: "Pending", 3: "Maintenance" };

const fmtTime = ts => new Date(ts * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });

function uptimeCell(u) {
  if (u === null || u === undefined) return '<span class="uptime">–</span>';
  const pct = u * 100;
  const cls = pct >= 99 ? "good" : (pct < 95 ? "bad" : "");
  const txt = pct >= 99.995 ? "100%" : pct.toFixed(2) + "%";
  return `<span class="uptime ${cls}" title="uptime, last 30 days">${txt}</span>`;
}

function barsHTML(bars) {
  return '<span class="bars" title="last 30 minutes">' + bars.map(b => {
    const cls = b.status === 1 ? "up" : b.status === 0 ? "down" : "";
    const label = new Date(b.ts * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
      + " — " + (b.status === 1 ? "up" : b.status === 0 ? "down" : "no data");
    return `<i class="${cls}" title="${label}"></i>`;
  }).join("") + "</span>";
}

function render(d) {
  document.title = d.page.title + " — Status";
  const o = OVERALL[d.overall] || OVERALL.up;

  let html = `
    <div class="status-header">
      <div class="logo">TSUNDERE<small>status</small></div>
      <h1>${esc(d.page.title)}</h1>
      ${d.page.description ? `<p class="muted">${esc(d.page.description)}</p>` : ""}
    </div>
    <div class="overall ${o.cls}">
      <span class="dot ${o.dot}"></span>
      <div>${o.text}<small>${o.sub}</small></div>
    </div>`;

  for (const m of d.maintenances) {
    html += `
      <div class="card notice">
        <h3>🔧 ${esc(m.title)} ${m.active ? '<span class="badge maint">active</span>' : '<span class="badge pending">scheduled</span>'}</h3>
        <div class="meta">${fmtTime(m.start_at)} → ${fmtTime(m.end_at)}</div>
        ${m.body ? `<p>${esc(m.body)}</p>` : ""}
      </div>`;
  }

  for (const i of d.incidents) {
    html += `
      <div class="card notice incident-${esc(i.severity)}">
        <h3>${esc(i.title)} ${i.resolved_at ? '<span class="resolved-tag">✓ resolved</span>' : `<span class="badge down">${esc(i.severity)}</span>`}</h3>
        <div class="meta">${fmtTime(i.created_at)}${i.resolved_at ? " — resolved " + fmtTime(i.resolved_at) : ""}</div>
        ${i.body ? `<p>${esc(i.body)}</p>` : ""}
      </div>`;
  }

  html += '<div class="card">';
  if (!d.monitors.length) {
    html += '<p class="empty">Nothing to show here yet. H-how embarrassing…</p>';
  }
  for (const m of d.monitors) {
    html += `
      <div class="svc">
        <span class="dot ${STATUS_DOT[m.status] || "pending"}" title="${STATUS_LABEL[m.status] || ""}"></span>
        <span class="name">${esc(m.name)}</span>
        ${barsHTML(m.bars)}
        ${uptimeCell(m.uptime_30d)}
      </div>`;
  }
  html += "</div>";

  html += `<div class="status-footer">
    Updated ${new Date(d.generated_at * 1000).toLocaleTimeString()} · refreshes automatically ·
    powered by <a href="https://github.com/4G0NYY/TSUNDERE">TSUNDERE</a>
    <br>(it's not like this page refreshes every 30 seconds just for you… baka.)
  </div>`;

  app.innerHTML = html;
}

async function load() {
  try {
    const resp = await fetch("/api/status/" + encodeURIComponent(slug));
    if (!resp.ok) {
      app.innerHTML = '<div class="card"><p class="empty">404 — there is no status page here. Did you mistype the URL? Hmph.</p></div>';
      return;
    }
    render(await resp.json());
  } catch (e) {
    app.innerHTML = '<div class="card"><p class="empty">Could not reach the server… t-that\'s ironic, isn\'t it.</p></div>';
  }
}

load();
setInterval(load, 30000);
