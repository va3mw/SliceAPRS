"use strict";
// SliceAPRS GUI: talks to the same JSON API in the browser and in the desktop app.

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const hhmm = (t) => new Date(t).toISOString().slice(11, 16);
const hhmmss = (t) => new Date(t).toISOString().slice(11, 19);
const ymdhm = (t) => new Date(t).toISOString().slice(0, 16).replace("T", " ");
const clone = (o) => JSON.parse(JSON.stringify(o));
const km = (v) => Math.round(v).toLocaleString() + " km";
const compass = (d) => ["N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"][Math.round(((d % 360) + 360) % 360 / 22.5) % 16];
const ago = (t) => {
  const m = Math.round((Date.now() - new Date(t)) / 60000);
  return m < 1 ? "just now" : m < 60 ? m + " min ago" : m < 1440 ? Math.floor(m / 60) + " h " + (m % 60) + " min ago" : Math.floor(m / 1440) + " d ago";
};
const levelWord = { normal: "Normal", enhanced: "Enhanced", open: "Open" };

// Distance bands share one blue ramp: darker (light mode) is farther.
const bands = [[50, "d1", "under 50 km"], [150, "d2", "50–150"], [300, "d3", "150–300"], [500, "d4", "300–500"], [Infinity, "d5", "over 500 km"]];
const band = (v) => bands.find((b) => v < b[0])[1];
const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue("--" + n).trim();
$("#kmLegend").innerHTML = bands.map((b) => `<span><i class="sw ${b[1]}"></i>${b[2]}</span>`).join("");

let cfg = null, local = false, status = null, overview = null;

async function api(path, body) {
  const opt = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const r = await fetch(path, opt);
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `${r.status} ${r.statusText}`);
  return j;
}

// ---------- tooltip ----------
const tip = $("#tip");
function showTip(e, html) {
  tip.innerHTML = html;
  tip.hidden = false;
  const x = Math.min(e.clientX + 14, innerWidth - tip.offsetWidth - 8);
  const y = Math.min(e.clientY + 14, innerHeight - tip.offsetHeight - 8);
  tip.style.left = x + "px";
  tip.style.top = y + "px";
}
document.addEventListener("mousemove", (e) => {
  const el = e.target.closest("[data-tip]");
  if (el) showTip(e, el.dataset.tip); else tip.hidden = true;
});

// ---------- tabs ----------
$$(".tabs button").forEach((b) => b.addEventListener("click", () => showTab(b.dataset.tab)));
function showTab(name) {
  $$(".tabs button").forEach((b) => b.classList.toggle("on", b.dataset.tab === name));
  $$(".tab").forEach((t) => t.classList.toggle("on", t.id === "tab-" + name));
  try { localStorage.setItem("tab", name); } catch {}
  if (name === "log") pollLog();
  if (name === "stations") pollStations();
  if (name === "grids") pollGrids();
  if (name === "openings") { pollOverview(); if (map) setTimeout(() => map.invalidateSize(), 50); }
}

setInterval(() => { $("#utc").textContent = new Date().toISOString().slice(11, 19); }, 500);

// ---------- status ----------
const stateWords = {
  setup: "Needs setup", searching: "Searching", connecting: "Connecting", running: "Receiving",
  replay: "Replaying", done: "Replay done", error: "Retrying", stopped: "Stopped",
};

async function pollStatus() {
  try {
    status = await api("/api/status");
    renderStatus(status);
  } catch (e) {
    $("#state").textContent = "No connection";
    $("#state").className = "pill error";
  }
}

function renderStatus(s) {
  $("#state").textContent = stateWords[s.state] || s.state;
  $("#state").className = "pill " + s.state;
  $("#state").title = s.error || "";
  $("#freq").textContent = s.freq_hz ? (s.freq_hz / 1e6).toFixed(3) + " MHz" : "–";
  const bits = [];
  if (s.replay) bits.push("replay " + s.replay.split(/[\\/]/).pop());
  else {
    if (s.radio) bits.push(s.radio);
    if (s.daxiq) bits.push("DAX IQ " + s.daxiq);
    if (s.rxant) bits.push(s.rxant);
  }
  $("#rxinfo").textContent = bits.join(" · ");
  $("#station").textContent = cfg && cfg.callsign ? `${cfg.callsign} · ${cfg.grid}` : "";
  $("#helpVersion").textContent = "version " + s.version;
  const d = s.decoder || {};
  $("#decStats").textContent = `Decoder: ${d.frames || 0} frames` + (d.bad_ax25 ? `, ${d.bad_ax25} not readable` : "") +
    (d.lost ? `, ${d.lost} IQ packets lost` : "") + (d.has_floor ? `, noise floor ${d.floor_db.toFixed(1)} dBFS` : "") +
    (d.slicers && d.slicers.length ? ` · per slicer ${d.slicers.join(" ")}` : "") + (s.others && s.others.length ? ` · also on the radio: ${s.others.join(", ")}` : "");

  const banner = $("#banner");
  if (s.state === "setup") {
    banner.innerHTML = "Welcome. Enter your callsign and grid in <b>Settings</b> and press <b>Save and restart</b> to start receiving.";
    banner.hidden = false;
  } else if (s.state === "error" && s.error) {
    banner.textContent = s.error;
    banner.hidden = false;
  } else if (!s.config_saved && !s.replay) {
    banner.textContent = "Running on default settings. Save them in Settings to keep them.";
    banner.hidden = false;
  } else banner.hidden = true;
}

// ---------- openings ----------
let hiddenLevel = "";
$("#alertHide").addEventListener("click", () => { hiddenLevel = overview ? overview.state.level : ""; $("#alert").hidden = true; });

async function pollOverview() {
  try { overview = await api("/api/overview"); } catch { return; }
  renderOverview(overview);
}

function farText(p) { return p ? `${esc(p.call)} · ${km(p.km)}` : "–"; }
function farSub(p, o) {
  if (!p) return "no direct paths";
  const r = o.state.normal_ok && o.state.normal_km ? ` · ${(p.km / o.state.normal_km).toFixed(1)}× normal` : "";
  return `${Math.round(p.brg)}° ${compass(p.brg)} · ${hhmm(p.t)} UTC${r}` + (p.src ? ` · repeating ${esc(p.src)}` : "");
}

function renderOverview(o) {
  const s = o.state;
  const lv = s.level || "normal";
  $("#level").textContent = levelWord[lv];
  $("#level").className = "lv-badge " + lv;
  $("#lvCard").className = "card lvcard " + lv;
  const cur = s.current;
  $("#levelText").textContent = s.satellite ? "satellite channel: nothing counts"
    : cur ? `since ${hhmm(cur.start)} UTC · ${cur.stations.length} far station${cur.stations.length === 1 ? "" : "s"}`
    : `${s.paths_hour} direct paths from ${s.stations_hour} stations this hour`;
  $("#far15").innerHTML = farText(s.far15);
  $("#far15s").textContent = farSub(s.far15, o);
  $("#far60").innerHTML = farText(s.far60);
  $("#far60s").textContent = farSub(s.far60, o);
  if (s.normal_ok) {
    $("#normal").textContent = km(s.normal_km);
    $("#normals").textContent = `far beyond ${km(s.far_km)}${s.pooled ? " · all hours (still learning this hour)" : ""} · ${s.usual} usual stations`;
  } else {
    $("#normal").textContent = "learning";
    $("#normals").textContent = `needs a day or two of history; until then only paths over ${km(s.open_km)} count`;
  }

  // Alert banner while the band is up.
  const al = $("#alert");
  if (lv !== "normal" && cur && hiddenLevel !== lv) {
    hiddenLevel = "";
    al.className = "alert " + lv;
    $("#alertTitle").textContent = lv === "open" ? "2 m is open" : "2 m is enhanced";
    const ratio = cur.normal_km ? ` (${(cur.max_km / cur.normal_km).toFixed(1)}× normal)` : "";
    $("#alertText").textContent = `toward ${cur.direction} · ${cur.stations.length} station${cur.stations.length === 1 ? "" : "s"} beyond normal · farthest ${cur.max_call} ${km(cur.max_km)}${ratio} · ${cur.strength} · since ${hhmm(cur.start)} UTC`;
    al.hidden = false;
  } else if (lv === "normal") {
    al.hidden = true;
    hiddenLevel = "";
  }

  // Latest path per station in the last hour, newest last.
  const latest = new Map();
  for (const p of o.paths) latest.set(p.call, p);
  const paths = [...latest.values()].sort((a, b) => new Date(a.t) - new Date(b.t));
  drawMap(o, paths);
  drawPolar(o, paths);
  drawHeat(o);
  drawOpenings(o);
}

// Map: Leaflet from a CDN; tiles need internet. Without either, the map
// panel says so and the plots carry on.
let map = null, mapLayer = null, mapState = "loading", tileOK = 0, tileErr = 0;
function loadLeaflet() {
  const css = document.createElement("link");
  css.rel = "stylesheet";
  css.href = "https://unpkg.com/leaflet@1.9.4/dist/leaflet.css";
  document.head.appendChild(css);
  const js = document.createElement("script");
  js.src = "https://unpkg.com/leaflet@1.9.4/dist/leaflet.js";
  js.onload = () => { mapState = "ready"; if (overview) renderOverview(overview); };
  js.onerror = () => mapOff("The map needs internet (Leaflet and map tiles could not be loaded). The plots work without it.");
  document.head.appendChild(js);
  setTimeout(() => { if (mapState === "loading") mapOff("The map needs internet; showing the plots only."); }, 15000);
}
function mapOff(why) {
  mapState = "off";
  $("#map").className = "mapoff";
  $("#map").textContent = why;
}

function drawMap(o, paths) {
  $("#mapNote").textContent = `${paths.length} station${paths.length === 1 ? "" : "s"}`;
  if (mapState !== "ready" || !window.L) return;
  if (!map) {
    map = L.map("map", { worldCopyJump: true }).setView([o.home_lat, o.home_lon], 7);
    const tiles = L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png",
      { maxZoom: 13, attribution: '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors' }).addTo(map);
    tiles.on("tileload", () => tileOK++);
    tiles.on("tileerror", () => { if (++tileErr >= 6 && !tileOK) { map.remove(); map = null; mapOff("Map tiles need internet; showing the plots only."); } });
    mapLayer = L.layerGroup().addTo(map);
    map._fitted = false;
  }
  mapLayer.clearLayers();
  L.circleMarker([o.home_lat, o.home_lon], { radius: 7, color: cssVar("panel") || "#fff", weight: 2, fillColor: cssVar("aub"), fillOpacity: 1 })
    .bindTooltip("You · " + (cfg ? cfg.grid : "")).addTo(mapLayer);
  const now = Date.now();
  const pts = [[o.home_lat, o.home_lon]];
  for (const p of paths) {
    const age = (now - new Date(p.t)) / 60000;
    const col = cssVar(band(p.km));
    const fresh = age < 5;
    L.polyline([[o.home_lat, o.home_lon], [p.lat, p.lon]], { color: col, weight: fresh ? 4 : 2, opacity: fresh ? 1 : Math.max(0.35, 1 - age / 75) }).addTo(mapLayer);
    L.circleMarker([p.lat, p.lon], { radius: fresh ? 6 : 4, color: "#fff", weight: 2, fillColor: col, fillOpacity: 1 })
      .bindTooltip(`<b>${esc(p.call)}</b> ${km(p.km)} · ${Math.round(p.brg)}° ${compass(p.brg)}<br>${hhmm(p.t)} UTC${p.src ? " · repeating " + esc(p.src) : ""}${p.hl ? ` · SNR ${p.snr.toFixed(0)} dB` : ""}`)
      .addTo(mapLayer);
    pts.push([p.lat, p.lon]);
  }
  if (!map._fitted && pts.length > 1) {
    map.fitBounds(pts, { padding: [24, 24], maxZoom: 9 });
    map._fitted = true;
  }
}

// Polar plot: bearing (north up) against distance.
function niceStep(max) {
  const raw = max / 4;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  return [1, 2, 2.5, 5, 10].map((m) => m * p).find((s) => s >= raw);
}
function drawPolar(o, paths) {
  const N = 420, c = N / 2, R = c - 26;
  const normal = o.state.normal_ok ? o.state.normal_km : 0;
  const maxKm = Math.max(100, normal * 1.6, ...paths.map((p) => p.km * 1.08));
  const step = niceStep(maxKm);
  const top = Math.ceil(maxKm / step) * step;
  const r = (v) => (v / top) * R;
  let svg = `<svg viewBox="0 0 ${N} ${N}" role="img" aria-label="Bearing and distance of direct paths">`;
  for (let v = step; v <= top + 1e-9; v += step) {
    svg += `<circle class="ring" cx="${c}" cy="${c}" r="${r(v)}"/><text x="${c + 3}" y="${c - r(v) - 3}">${Math.round(v)}</text>`;
  }
  for (let a = 0; a < 360; a += 45) {
    const x = c + R * Math.sin(a * Math.PI / 180), y = c - R * Math.cos(a * Math.PI / 180);
    svg += `<line class="ring" x1="${c}" y1="${c}" x2="${x}" y2="${y}"/>`;
    const lx = c + (R + 14) * Math.sin(a * Math.PI / 180), ly = c - (R + 14) * Math.cos(a * Math.PI / 180) + 4;
    svg += `<text x="${lx}" y="${ly}" text-anchor="middle">${compass(a)}</text>`;
  }
  if (normal) svg += `<circle class="normal" cx="${c}" cy="${c}" r="${r(normal)}"><title>normal ${km(normal)}</title></circle>`;
  const now = Date.now();
  for (const p of paths) {
    const x = c + r(p.km) * Math.sin(p.brg * Math.PI / 180), y = c - r(p.km) * Math.cos(p.brg * Math.PI / 180);
    const fresh = (now - new Date(p.t)) / 60000 < 5;
    const t = `<b>${esc(p.call)}</b> ${km(p.km)} · ${Math.round(p.brg)}° ${compass(p.brg)}<br>${hhmm(p.t)} UTC${p.hl ? ` · SNR ${p.snr.toFixed(0)} dB` : ""}`;
    svg += `<circle class="dot" cx="${x}" cy="${y}" r="5" fill="var(--${band(p.km)})"/>`;
    if (fresh) svg += `<circle class="new" cx="${x}" cy="${y}" r="9"/>`;
    svg += `<circle cx="${x}" cy="${y}" r="11" fill="transparent" data-tip="${esc(t)}"/>`;
  }
  svg += `<circle cx="${c}" cy="${c}" r="4" fill="var(--aub-3)"/></svg>`;
  $("#polar").innerHTML = svg;
  $("#polarNote").textContent = paths.length ? "" : "nothing heard directly yet";
}

// 24 h: farthest direct each hour, coloured by how far beyond normal.
function drawHeat(o) {
  const day = o.day;
  const head = `<div class="hb"></div>` + day.map((h) => `<div class="hh">${String(new Date(h.start).getUTCHours()).padStart(2, "0")}</div>`).join("");
  const far = `<div class="hb">Farthest</div>` + day.map((h) => {
    const t = `${hhmm(h.start)} UTC`;
    if (!h.packets && !h.max_km) return `<div class="hc none" data-tip="${t}: nothing decoded"></div>`;
    if (!h.max_km) return `<div class="hc r0" data-tip="${t}: ${h.packets} packets, no direct path with a known position">·</div>`;
    const ratio = h.normal_ok && h.normal_km ? h.max_km / h.normal_km : 0;
    const cls = !ratio || ratio <= 1 ? "r0" : ratio <= 1.3 ? "r1" : ratio <= 2 ? "r2" : "r3";
    return `<div class="hc ${cls}" data-tip="<b>${t}</b><br>farthest ${esc(h.max_call)} ${km(h.max_km)}${ratio ? ` (${ratio.toFixed(1)}× normal)` : ""}<br>${h.stations} stations direct · ${h.packets} packets">${Math.round(h.max_km)}</div>`;
  }).join("");
  const norm = `<div class="hb">Normal</div>` + day.map((h) =>
    `<div class="hc nm" data-tip="${hhmm(h.start)} UTC: normal ${h.normal_ok ? km(h.normal_km) : "not learned yet"}">${h.normal_ok ? Math.round(h.normal_km) : "–"}</div>`).join("");
  $("#heat").innerHTML = head + far + norm;
}

function dur(a, b) {
  const m = Math.max(0, Math.round((new Date(b) - new Date(a)) / 60000));
  return m < 60 ? m + " min" : Math.floor(m / 60) + " h " + (m % 60) + " min";
}
function drawOpenings(o) {
  const list = o.openings || [];
  $("#noOpenings").hidden = list.length > 0;
  $("#openNote").textContent = list.length ? `${list.length} logged` : "";
  $("#openings tbody").innerHTML = list.map((x) => `<tr class="${x.active ? "far" : ""}">
    <td class="mono">${ymdhm(x.start)}</td><td class="mono">${x.active ? "<b>now</b>" : hhmm(x.end)}</td>
    <td class="r">${dur(x.start, x.active ? Date.now() : x.end)}</td>
    <td><span class="lv-badge ${x.peak}">${levelWord[x.peak] || x.peak}</span></td><td>${esc(x.strength)}</td>
    <td>${esc(x.direction)}${x.direction !== "several directions" ? ` (${Math.round(x.bearing)}°)` : ""}</td>
    <td>${esc(x.max_call)} ${km(x.max_km)}</td><td class="r">${x.stations.length}</td>
    <td class="wrap small">${x.stations.slice(0, 12).map((s) => `${esc(s.call)} ${Math.round(s.km)}`).join(", ")}${x.stations.length > 12 ? " …" : ""}</td></tr>`).join("");
}

// ---------- stations ----------
let stations = [];
async function pollStations() {
  if (!$("#tab-stations").classList.contains("on")) return;
  try { stations = await api("/api/stations"); } catch { return; }
  renderStations();
}
function heardText(s) {
  if (s.last_via === "") return `<span class="kind direct">direct</span>`;
  if (["gateway", "satellite", "own", "via-alias"].includes(s.last_via)) return `<span class="kind">${esc(s.last_via)}</span>`;
  return `<span class="kind digi">via ${esc(s.last_via)}</span>`;
}
function renderStations() {
  const q = $("#stFind").value.trim().toUpperCase();
  const direct = $("#stDirect").checked;
  const list = stations.filter((s) => (!q || s.call.includes(q)) && (!direct || s.direct > 0));
  $("#stNote").textContent = `${list.length} of ${stations.length}`;
  $("#stations tbody").innerHTML = list.slice(0, 1000).map((s) => `<tr>
    <td class="call">${esc(s.call)}${s.digi ? ' <span class="kind">digi</span>' : ""}${s.usual ? ' <span class="kind usual">usual</span>' : ""}</td>
    <td class="mono small">${s.has_pos ? `${s.lat.toFixed(4)} ${s.lon.toFixed(4)}` : '<span class="muted">unknown</span>'}${s.pending ? ` <span class="kind jump" data-tip="Unconfirmed report ${km(s.pending.km)} away at ${hhmm(s.pending.time)} UTC: waiting for a second report">jump?</span>` : ""}</td>
    <td class="r">${s.has_pos ? Math.round(s.km) : ""}</td><td class="r">${s.has_pos ? Math.round(s.bearing) + "° " + compass(s.bearing) : ""}</td>
    <td>${heardText(s)}</td><td data-tip="${esc(ymdhm(s.last_heard))} UTC">${ago(s.last_heard)}</td>
    <td class="r">${s.packets}</td><td class="r">${s.direct}</td><td class="r">${s.best_km ? Math.round(s.best_km) + " km" : ""}</td>
    <td class="small muted wrap">${esc(s.comment || "")}</td></tr>`).join("");
}
$("#stFind").addEventListener("input", renderStations);
$("#stDirect").addEventListener("change", renderStations);

// ---------- grids ----------
let gridData = null;
$("#grLegend").innerHTML = bands.map((b) => `<span><i class="sw ${b[1]}"></i>${b[2]}</span>`).join("");
async function pollGrids() {
  if (!$("#tab-grids").classList.contains("on")) return;
  try { gridData = await api("/api/grids"); } catch { return; }
  renderGrids();
}
$("#grPeriod").addEventListener("change", renderGrids);
const gridSW = (g) => [(g.charCodeAt(0) - 65) * 20 - 180 + (+g[2]) * 2, (g.charCodeAt(1) - 65) * 10 - 90 + (+g[3])]; // lon, lat of SW corner
const gridName = (lon, lat) => {
  const x = lon + 180, y = lat + 90;
  return String.fromCharCode(65 + Math.floor(x / 20), 65 + Math.floor(y / 10)) + Math.floor((x % 20) / 2) + Math.floor(y % 10);
};
function renderGrids() {
  if (!gridData) return;
  const hours = +$("#grPeriod").value;
  const since = hours ? Date.now() - hours * 3600000 : 0;
  const list = gridData.grids.filter((g) => new Date(g.last) >= since);
  $("#grNote").textContent = `${list.length} grid${list.length === 1 ? "" : "s"}` + (gridData.home ? ` � you are in ${gridData.home.slice(0, 4)}` : "");
  $("#noGrids").hidden = list.length > 0;
  $("#grids tbody").innerHTML = list.map((g) => `<tr>
    <td class="call">${esc(g.grid)}</td><td class="r">${km(g.best_km)}</td><td class="r">${Math.round(g.bearing)}� ${compass(g.bearing)}</td>
    <td>${esc(g.best_call)}</td><td class="r" data-tip="${esc(g.calls.join(", "))}">${g.calls.length}</td><td class="r">${g.count}</td>
    <td class="mono">${ymdhm(g.first)}</td><td data-tip="${esc(ymdhm(g.last))} UTC">${ago(g.last)}</td></tr>`).join("");
  drawGridMap(list);
}
function drawGridMap(list) {
  const hlat = gridData.home_lat, hlon = gridData.home_lon;
  if (!hlat && !hlon) { $("#gridMap").innerHTML = '<p class="muted">Set your grid in Settings.</p>'; return; }
  // Extent: your square and every heard square, one square of margin, at least 10 x 6 squares.
  const home = gridSW(gridName(hlon, hlat));
  let w = home[0], e = home[0] + 2, s = home[1], n = home[1] + 1;
  for (const g of list) { const [lo, la] = gridSW(g.grid); w = Math.min(w, lo); e = Math.max(e, lo + 2); s = Math.min(s, la); n = Math.max(n, la + 1); }
  w -= 2; e += 2; s -= 1; n += 1;
  while (e - w < 20) { w -= 2; e += 2; }
  while (n - s < 6) { s -= 1; n += 1; }
  const k = Math.cos(((n + s) / 2) * Math.PI / 180); // keep squares the right shape
  const W = (e - w) * k, H = n - s;
  const box = $("#gridMap").getBoundingClientRect();
  const sc = Math.min((box.width || 900) / W, (box.height || 480) / H);
  const X = (lon) => (lon - w) * k * sc, Y = (lat) => (n - lat) * sc;
  const cw = 2 * k * sc, ch = sc;
  const heard = new Map(list.map((g) => [g.grid, g]));
  let svg = `<svg viewBox="0 0 ${W * sc} ${H * sc}" role="img" aria-label="Grid squares heard directly">`;
  for (let lon = w; lon < e; lon += 2) {
    for (let lat = s; lat < n; lat++) {
      const name = gridName(lon + 1, lat + 0.5);
      const g = heard.get(name);
      const x = X(lon), y = Y(lat + 1);
      if (g) {
        const b = band(g.best_km);
        const tip = `<b>${name}</b> � farthest ${esc(g.best_call)} ${km(g.best_km)} ${compass(g.bearing)}<br>${g.calls.length} station${g.calls.length === 1 ? "" : "s"} � ${g.count} paths<br>last ${ymdhm(g.last)} UTC`;
        svg += `<rect class="heard" x="${x}" y="${y}" width="${cw}" height="${ch}" fill="var(--${b})" data-tip="${esc(tip)}"/>`;
        if (cw > 34) svg += `<text class="hlab${b === "d1" || b === "d2" ? " lt" : ""}" x="${x + cw / 2}" y="${y + ch / 2 + 4}" text-anchor="middle" pointer-events="none">${name}</text>`;
      } else {
        svg += `<rect class="gl" x="${x}" y="${y}" width="${cw}" height="${ch}"/>`;
        if (cw > 40) svg += `<text class="glab" x="${x + cw / 2}" y="${y + ch / 2 + 4}" text-anchor="middle">${name}</text>`;
      }
    }
  }
  svg += `<rect class="home" x="${X(home[0])}" y="${Y(home[1] + 1)}" width="${cw}" height="${ch}"/>`;
  svg += `<circle class="homedot" cx="${X(hlon)}" cy="${Y(hlat)}" r="4"><title>You</title></circle></svg>`;
  $("#gridMap").innerHTML = svg;
}
window.addEventListener("resize", () => { if ($("#tab-grids").classList.contains("on")) renderGrids(); });

// ---------- packets ----------
let pkSeq = 0, pkRows = [];
async function pollPackets() {
  if ($("#pkPause").checked) return;
  let add;
  try { add = await api("/api/packets?since=" + pkSeq); } catch { return; }
  if (!add || !add.length) return;
  if (add[0].seq <= pkSeq) { pkRows = []; } // restarted
  pkSeq = add.at(-1).seq;
  pkRows = pkRows.concat(add).slice(-500);
  renderPackets();
}
function viaText(h) {
  switch (h.kind) {
    case "direct": return `<span class="kind direct">direct</span> ${esc(h.via)}`;
    case "digi": return `<span class="kind direct">direct</span> ${esc(h.via)} <span class="muted small">(digi)</span>`;
    case "via-alias": return `<span class="kind">unknown digi</span>`;
    default: return `<span class="kind">${esc(h.kind)}</span>`;
  }
}
function renderPackets() {
  const raw = $("#pkRaw").checked;
  $("#pkNote").textContent = `${pkRows.length} shown`;
  $("#packets tbody").innerHTML = pkRows.slice().reverse().map((h) => {
    let note = h.note ? `<div class="small muted">${esc(h.note)}</div>` : "";
    if (h.jump && h.last_pos && h.pkt_pos) {
      note = `<div class="small"><span class="kind jump">position jump</span> last known ${h.last_pos.lat.toFixed(4)} ${h.last_pos.lon.toFixed(4)} (${km(h.last_pos.km)}) · this packet ${h.pkt_pos.lat.toFixed(4)} ${h.pkt_pos.lon.toFixed(4)} (${km(h.pkt_pos.km)}) · not counted</div>`;
    }
    return `<tr class="${h.counted && overview && overview.state.far_km && h.km > overview.state.far_km ? "far" : ""}">
    <td class="mono">${hhmmss(h.time)}</td><td>${viaText(h)}</td>
    <td class="r">${h.path_known ? Math.round(h.km) : ""}</td><td class="r">${h.has_level ? h.snr.toFixed(0) : ""}</td>
    <td class="call">${esc(h.src)}</td>
    <td class="wrap">${esc(h.summary || h.error || "")}${note}${raw ? `<div class="raw">${esc(h.raw)}</div>` : ""}</td></tr>`;
  }).join("");
}
$("#pkRaw").addEventListener("change", renderPackets);

// ---------- log ----------
let logSeq = 0;
async function pollLog() {
  if (!$("#tab-log").classList.contains("on")) return;
  try {
    const j = await api("/api/log?since=" + logSeq);
    if (j.seq < logSeq) { logSeq = 0; $("#log").textContent = ""; }
    logSeq = j.seq;
    if (j.lines.length) {
      const pre = $("#log");
      const frag = document.createDocumentFragment();
      for (const l of j.lines) {
        const d = document.createElement("div");
        const m = l.match(/level=(\w+)/);
        if (m) d.className = m[1];
        d.textContent = l;
        frag.appendChild(d);
      }
      pre.appendChild(frag);
      while (pre.childNodes.length > 1000) pre.removeChild(pre.firstChild);
      if ($("#follow").checked) pre.scrollTop = pre.scrollHeight;
    }
  } catch {}
}

// ---------- settings ----------
const getPath = (o, p) => p.split(".").reduce((a, k) => (a == null ? a : a[k]), o);
function setPath(o, p, v) {
  const ks = p.split(".");
  let a = o;
  ks.slice(0, -1).forEach((k) => (a = a[k] ??= {}));
  a[ks.at(-1)] = v;
}
const defaultAnts = ["ANT1", "ANT2", "RX_A", "RX_B", "XVTA", "XVTB"];
const antLabel = (a) => ({ RX_A: "RXa", RX_B: "RXb", XVTA: "XVTa", XVTB: "XVTb" }[a] || a);
function fillAnts(value) {
  const sel = $("#settings").elements["rxant"];
  const radio = status && status.antennas && status.antennas.length ? status.antennas : null;
  const list = [...(radio || defaultAnts)];
  if (value && !list.includes(value)) list.push(value);
  sel.innerHTML = `<option value="">Radio's choice (the transverter's when one is set up)</option>` +
    list.map((a) => `<option value="${esc(a)}">${esc(antLabel(a))}</option>`).join("");
  sel.value = value || "";
  $("#antNote").textContent = radio ? "Antennas as reported by the radio." : "Connect to the radio to read its antenna list.";
}

function fillWeb(listen) {
  const m = /^(.*):(\d+)$/.exec(listen || "");
  $("#webPort").value = m ? m[2] : "";
  $("#webLan").checked = m ? !/^(127\.0\.0\.1|localhost|\[::1\])$/.test(m[1]) : false;
  webNote();
}
function readWeb() {
  const p = parseInt($("#webPort").value, 10);
  if (!p) return "off";
  return ($("#webLan").checked ? "" : "127.0.0.1") + ":" + p;
}
function webNote() {
  const p = parseInt($("#webPort").value, 10);
  $("#webNote").textContent = !p ? "No web page."
    : $("#webLan").checked ? `Other computers: http://<this computer>:${p}/ (no password; keep it on your LAN).`
    : `This computer only: http://localhost:${p}/`;
  if (!local) $("#webNote").textContent += " Change it in the config file or the desktop app.";
}
$("#webPort").addEventListener("input", webNote);
$("#webLan").addEventListener("change", webNote);

const rulesText = (r, d) => `far: beyond normal × ${r.factor} (and at least ${r.margin} km beyond it), not a usual station · Enhanced: ${r.enhanced} far station${r.enhanced > 1 ? "s" : ""} in 30 min · Open: ${r.open} far stations, or any direct path over ${d} km`;
const rules = { low: { factor: 1.5, margin: 60, enhanced: 2, open: 4 }, medium: { factor: 1.3, margin: 40, enhanced: 1, open: 3 }, high: { factor: 1.15, margin: 25, enhanced: 1, open: 2 } };
function sensNote() {
  const f = $("#settings").elements;
  $("#sensNote").textContent = rulesText(rules[f["openings.sensitivity"].value] || rules.medium, f["openings.distance_km"].value || 300);
}
$("#settings").elements["openings.sensitivity"].addEventListener("change", sensNote);
$("#settings").elements["openings.distance_km"].addEventListener("input", sensNote);
$("#rulesTable").innerHTML = Object.entries(rules).map(([k, r]) => `<dt>${k[0].toUpperCase() + k.slice(1)}</dt><dd>${rulesText(r, "the set distance")}</dd>`).join("");

$("#freqSel").addEventListener("change", () => {
  const v = $("#freqSel").value;
  $("#freqOther").hidden = v !== "other";
  if (v !== "other") $("#settings").elements["frequency_mhz"].value = v;
});

function fillSettings() {
  const f = $("#settings");
  fillAnts(cfg.rxant);
  for (const el of f.elements) {
    if (!el.name || el.name === "rxant") continue;
    const v = getPath(cfg, el.name);
    if (el.type === "checkbox") el.checked = !!v;
    else el.value = v ?? "";
  }
  const fm = String(+cfg.frequency_mhz);
  const known = [...$("#freqSel").options].some((o) => o.value === fm);
  $("#freqSel").value = known ? fm : "other";
  $("#freqOther").hidden = known;
  fillWeb(cfg.web.listen);
  $$(".sys", f).forEach((el) => (el.disabled = !local));
  $("#localNote").hidden = local;
  $("#cfgPath").textContent = status ? "Config file: " + status.config_path : "";
  sensNote();
}

function readSettings() {
  const c = clone(cfg);
  for (const el of $("#settings").elements) {
    if (!el.name) continue;
    let v;
    if (el.type === "checkbox") v = el.checked;
    else if (el.name === "daxiq.channel") v = parseInt(el.value, 10) || 0;
    else if (el.name === "frequency_mhz" || el.name === "openings.distance_km") v = parseFloat(el.value) || 0;
    else v = el.value.trim();
    setPath(c, el.name, v);
  }
  c.web.listen = readWeb();
  return c;
}

$("#saveSet").dataset.label = $("#saveSet").textContent;
$("#saveSet").addEventListener("click", async () => {
  const f = $("#settings");
  if (!f.reportValidity()) return;
  const btn = $("#saveSet");
  $("#setErr").textContent = "";
  btn.disabled = true;
  try {
    const j = await api("/api/config", readSettings());
    cfg = j.config;
    local = j.local;
    fillSettings();
    btn.textContent = "Saved ✓";
    setTimeout(() => (btn.textContent = btn.dataset.label), 1500);
    pollStatus();
  } catch (e) {
    $("#setErr").textContent = e.message;
  } finally {
    btn.disabled = false;
  }
});

$("#findRadios").addEventListener("click", async () => {
  const b = $("#findRadios");
  b.disabled = true;
  $("#findNote").textContent = "Listening for 3 s…";
  $("#radios").innerHTML = "";
  try {
    const list = await api("/api/radios");
    $("#findNote").textContent = list.length ? "Pick one:" : "No radios heard. Is it on this network?";
    $("#radios").innerHTML = list.map((r, i) =>
      `<button type="button" class="ghost" data-i="${i}"><b>${esc(r.Model)}</b> ${esc(r.Nickname)} · ${esc(r.IP)}
        <span class="muted small">serial ${esc(r.Serial)} · v${esc(r.Version)} · ${esc(r.Status)}</span></button>`).join("");
    $$("#radios button").forEach((btn) => btn.addEventListener("click", () => {
      $("#settings").elements["radio.ip"].value = list[btn.dataset.i].IP;
    }));
  } catch (e) {
    $("#findNote").textContent = e.message;
  } finally {
    b.disabled = false;
  }
});

// ---------- start ----------
(async function init() {
  try {
    const j = await api("/api/config");
    cfg = j.config;
    local = j.local;
  } catch (e) {
    $("#banner").textContent = "Cannot reach SliceAPRS: " + e.message;
    $("#banner").hidden = false;
    return;
  }
  await pollStatus();
  fillSettings();
  let tab = "openings";
  try { tab = localStorage.getItem("tab") || tab; } catch {}
  if (status && status.state === "setup") tab = "settings";
  showTab(tab);
  loadLeaflet();
  pollOverview();
  pollPackets();
  let antKey = "";
  setInterval(() => {
    pollStatus().then(() => {
      const k = (status && status.antennas || []).join(",");
      if (k && k !== antKey) { antKey = k; fillAnts($("#settings").elements["rxant"].value || cfg.rxant); }
    });
  }, 2000);
  setInterval(pollPackets, 2000);
  setInterval(pollLog, 2000);
  setInterval(pollOverview, 10000);
  setInterval(pollStations, 10000);
  setInterval(pollGrids, 30000);
})();
