package api

// dashboardHTML is a single self-contained page served at GET /. It polls the
// JSON API and renders the live value plus an interactive 48h-class chart:
// selectable time window (24h / 48h / 7 days), time axis labels, a hover tooltip
// with the bucket's avg/min/max, and a shaded min–max band. No external assets
// (no CDN), so it works offline at demos.
//
// Note: this is a Go raw string literal, so the embedded JS must NOT use
// backticks (template literals) — string concatenation is used throughout.
const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Temperature Monitor</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: system-ui, -apple-system, sans-serif;
         background:#0d1117; color:#e6edf3; }
  .wrap { max-width:760px; margin:0 auto; padding:28px 24px 40px; }
  h1 { font-size:.95rem; font-weight:600; color:#8b97a4; letter-spacing:.02em;
       margin:0 0 20px; }
  h1 span { color:#e6edf3; }
  #sensorsel { background:#161b22; color:#e6edf3; border:1px solid #30363d;
               border-radius:6px; padding:3px 8px; font-size:.95rem;
               font-family:inherit; cursor:pointer; }
  .reading { display:flex; align-items:baseline; gap:6px; }
  .val { font-size:4.5rem; font-weight:700; line-height:1; letter-spacing:-.02em;
         font-variant-numeric:tabular-nums; }
  .unit { font-size:1.5rem; color:#8b97a4; }
  .badge { font-size:.72rem; font-weight:700; padding:3px 10px; border-radius:999px;
           text-transform:uppercase; letter-spacing:.05em; align-self:center;
           margin-left:6px; }
  .live { background:#0f3d22; color:#3fb950; }
  .stale { background:#3d1518; color:#f85149; }
  .meta { margin-top:10px; color:#8b97a4; font-size:.85rem; }
  .win { display:inline-flex; margin:26px 0 6px; }
  .win button { background:#161b22; color:#8b97a4; border:1px solid #30363d;
                padding:6px 16px; font-size:.82rem; font-weight:600; cursor:pointer;
                transition:all .12s; }
  .win button:first-child { border-radius:7px 0 0 7px; }
  .win button:last-child { border-radius:0 7px 7px 0; }
  .win button + button { border-left:none; }
  .win button:hover { color:#e6edf3; }
  .win button.active { background:#1f6feb; color:#fff; border-color:#1f6feb; }
  #chartwrap { position:relative; margin-top:10px; }
  svg { width:100%; height:280px; display:block;
        background:#0b0f14; border:1px solid #1b232c; border-radius:10px; }
  .grid { stroke:#1b232c; stroke-width:1; }
  .band { fill:#3fb950; opacity:.13; }
  .line { fill:none; stroke:#3fb950; stroke-width:2; stroke-linejoin:round; }
  .hair { stroke:#6e7b8a; stroke-width:1; stroke-dasharray:3 3; }
  .axis { fill:#6e7b8a; font-size:11px; font-family:system-ui, sans-serif; }
  #tip { position:absolute; display:none; pointer-events:none; z-index:5;
         background:#1b232c; border:1px solid #30363d; border-radius:7px;
         padding:7px 10px; font-size:.78rem; line-height:1.4; white-space:nowrap;
         box-shadow:0 6px 18px rgba(0,0,0,.5); }
  #tip b { font-size:.92rem; }
  #tip .sub { color:#8b97a4; }
  .caption { margin-top:6px; color:#6e7b8a; font-size:.8rem; }
</style>
</head>
<body>
<div class="wrap">
  <h1>Temperature Monitor — <select id="sensorsel" title="sensor / room"></select></h1>
  <div class="reading">
    <span class="val" id="value">--</span><span class="unit">°C</span>
    <span class="badge" id="badge">…</span>
  </div>
  <div class="meta" id="meta">connecting…</div>

  <div class="win" id="windows"></div>
  <div id="chartwrap">
    <svg id="chart" viewBox="0 0 640 280" preserveAspectRatio="none"></svg>
    <div id="tip"></div>
  </div>
  <div class="caption" id="caption">history</div>
</div>
<script>
var SVGNS = "http://www.w3.org/2000/svg";
var params = new URLSearchParams(location.search);
var sensor = params.get("sensor_id") || "fermenter-1";
var sensorsKey = ""; // join of the last-rendered dropdown options

// window label -> {hours, bucket}. Bucket sized for a clean point count.
var WINDOWS = {
  "24h":    { hours: 24,  bucket: 600 },   // 144 points (10 min)
  "48h":    { hours: 48,  bucket: 600 },   // 288 points (10 min)
  "7 days": { hours: 168, bucket: 3600 }   // 168 points (1 hour)
};
var activeWindow = "48h";
var lastPoints = [];
var geom = null;
var DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function el(id) { return document.getElementById(id); }
function pad2(n) { return (n < 10 ? "0" : "") + n; }
function fmtHM(d) { return pad2(d.getHours()) + ":" + pad2(d.getMinutes()); }
function fmtAxis(d) {
  return activeWindow === "7 days" ? DOW[d.getDay()] + " " + fmtHM(d) : fmtHM(d);
}
function fmtFull(d) { return DOW[d.getDay()] + " " + fmtHM(d); }

async function refreshCurrent() {
  try {
    var r = await fetch("/api/current?sensor_id=" + encodeURIComponent(sensor));
    if (r.status === 404) {
      el("value").textContent = "--"; el("meta").textContent = "no data yet";
      el("badge").textContent = "no data"; el("badge").className = "badge stale";
      return;
    }
    var d = await r.json();
    el("value").textContent = d.value.toFixed(1);
    el("meta").textContent = "updated " + d.age_seconds + "s ago";
    if (d.stale) { el("badge").textContent = "stale"; el("badge").className = "badge stale"; }
    else { el("badge").textContent = "live"; el("badge").className = "badge live"; }
  } catch (e) { el("meta").textContent = "connection error"; }
}

async function refreshSensors() {
  try {
    var r = await fetch("/api/sensors");
    if (!r.ok) return;
    var d = await r.json();
    var names = d.sensors || [];
    if (names.indexOf(sensor) < 0) names = [sensor].concat(names); // keep current visible
    var key = names.join("|");
    if (key === sensorsKey) return; // unchanged — don't disturb an open dropdown
    sensorsKey = key;
    var sel = el("sensorsel");
    sel.innerHTML = "";
    names.forEach(function (nm) {
      var o = document.createElement("option");
      o.value = nm; o.textContent = nm;
      if (nm === sensor) o.selected = true;
      sel.appendChild(o);
    });
  } catch (e) {}
}

function setWindow(name) {
  activeWindow = name;
  var btns = el("windows").children;
  for (var i = 0; i < btns.length; i++) {
    btns[i].className = (btns[i].getAttribute("data-win") === name) ? "active" : "";
  }
  el("caption").textContent = "last " + name;
  refreshHistory();
}

async function refreshHistory() {
  var w = WINDOWS[activeWindow];
  try {
    var r = await fetch("/api/history?sensor_id=" + encodeURIComponent(sensor) +
                        "&hours=" + w.hours + "&bucket=" + w.bucket);
    if (!r.ok) return;
    var d = await r.json();
    lastPoints = d.points || [];
    drawChart(lastPoints, d.from, d.to);
  } catch (e) {}
}

function clearSvg(svg) { while (svg.firstChild) svg.removeChild(svg.firstChild); }
function mk(name, attrs) {
  var e = document.createElementNS(SVGNS, name);
  for (var k in attrs) e.setAttribute(k, attrs[k]);
  return e;
}
function axisText(svg, x, y, s, anchor) {
  var t = mk("text", { x: x, y: y, "class": "axis", "text-anchor": anchor || "middle" });
  t.textContent = s; svg.appendChild(t);
}

// drawChart plots points on a LINEAR time axis spanning [fromStr, toStr], so
// gaps in the data show as real gaps. The line/band are broken wherever buckets
// are missing (a time step larger than ~1.5 buckets) rather than drawn straight
// across — missing data is not faked as continuous.
function drawChart(points, fromStr, toStr) {
  var svg = el("chart"); clearSvg(svg); geom = null;
  if (!points || !points.length) return;

  var W = 640, H = 280, padL = 46, padR = 14, padT = 16, padB = 38;
  var plotW = W - padL - padR, plotH = H - padT - padB;

  var t0 = new Date(fromStr).getTime();
  var t1 = new Date(toStr).getTime();
  if (!(t1 > t0)) t1 = t0 + 1;

  var mins = points.map(function (p) { return p.min; });
  var maxs = points.map(function (p) { return p.max; });
  var times = points.map(function (p) { return new Date(p.t).getTime(); });
  var lo = Math.min.apply(null, mins), hi = Math.max.apply(null, maxs);
  if (hi - lo < 1) { var m = (hi + lo) / 2; lo = m - 0.5; hi = m + 0.5; }

  function px(tms) { return padL + (tms - t0) / (t1 - t0) * plotW; }
  function py(v) { return padT + (hi - v) * plotH / (hi - lo); }
  geom = { padL: padL, plotW: plotW, t0: t0, t1: t1, times: times };

  // horizontal grid + y-axis labels
  var GRID = 4;
  for (var g = 0; g <= GRID; g++) {
    var val = lo + (hi - lo) * g / GRID, yy = py(val);
    svg.appendChild(mk("line", { x1: padL, y1: yy, x2: W - padR, y2: yy, "class": "grid" }));
    axisText(svg, padL - 7, yy + 4, val.toFixed(1), "end");
  }

  // split into contiguous segments, breaking on missing buckets
  var bucketMs = WINDOWS[activeWindow].bucket * 1000;
  var gapMs = bucketMs * 1.5;
  var segments = [], cur = [0];
  for (var i = 1; i < points.length; i++) {
    if (times[i] - times[i - 1] > gapMs) { segments.push(cur); cur = []; }
    cur.push(i);
  }
  segments.push(cur);

  // draw a min–max band + average line per segment (isolated points -> a dot)
  segments.forEach(function (seg) {
    if (seg.length === 1) {
      var s = seg[0];
      svg.appendChild(mk("circle", { cx: px(times[s]).toFixed(1), cy: py(points[s].avg).toFixed(1),
                                     r: 1.8, fill: "#3fb950" }));
      return;
    }
    var band = "";
    for (var a = 0; a < seg.length; a++) band += px(times[seg[a]]).toFixed(1) + "," + py(maxs[seg[a]]).toFixed(1) + " ";
    for (var b = seg.length - 1; b >= 0; b--) band += px(times[seg[b]]).toFixed(1) + "," + py(mins[seg[b]]).toFixed(1) + " ";
    svg.appendChild(mk("polygon", { points: band, "class": "band" }));
    var line = seg.map(function (idx) { return px(times[idx]).toFixed(1) + "," + py(points[idx].avg).toFixed(1); }).join(" ");
    svg.appendChild(mk("polyline", { points: line, "class": "line" }));
  });

  // x-axis time labels, evenly spaced across the window (linear in time)
  var LBL = 6;
  for (var k = 0; k < LBL; k++) {
    var tk = t0 + (t1 - t0) * k / (LBL - 1);
    var anchor = k === 0 ? "start" : (k === LBL - 1 ? "end" : "middle");
    axisText(svg, px(tk), H - padB + 18, fmtAxis(new Date(tk)), anchor);
  }

  // hover hairline (hidden until mouse enters)
  svg.appendChild(mk("line", { id: "hair", x1: 0, y1: padT, x2: 0, y2: H - padB,
                               "class": "hair", visibility: "hidden" }));
}

function onMove(ev) {
  if (!geom || !lastPoints.length) return;
  var svg = el("chart"), rect = svg.getBoundingClientRect();
  var vbx = (ev.clientX - rect.left) / rect.width * 640;
  var tms = geom.t0 + (vbx - geom.padL) / geom.plotW * (geom.t1 - geom.t0);

  // nearest point by time
  var best = -1, bestd = Infinity;
  for (var i = 0; i < geom.times.length; i++) {
    var dd = Math.abs(geom.times[i] - tms);
    if (dd < bestd) { bestd = dd; best = i; }
  }
  // hovering an empty stretch (no bucket within ~1.5 buckets) -> no tooltip
  if (best < 0 || bestd > WINDOWS[activeWindow].bucket * 1000 * 1.5) { onLeave(); return; }

  var p = lastPoints[best];
  var x = geom.padL + (geom.times[best] - geom.t0) / (geom.t1 - geom.t0) * geom.plotW;

  var hair = el("hair");
  if (hair) { hair.setAttribute("x1", x); hair.setAttribute("x2", x); hair.setAttribute("visibility", "visible"); }

  var tip = el("tip");
  tip.innerHTML = "<b>" + p.avg.toFixed(1) + " °C</b><br>" +
                  "<span class='sub'>" + fmtFull(new Date(p.t)) + "</span><br>" +
                  "<span class='sub'>min " + p.min.toFixed(1) + "  max " + p.max.toFixed(1) + "</span>";
  var wrap = el("chartwrap"), wr = wrap.getBoundingClientRect();
  var left = ev.clientX - wr.left + 14, top = ev.clientY - wr.top + 14;
  if (left > wr.width - 150) left = ev.clientX - wr.left - 150;
  tip.style.left = left + "px"; tip.style.top = top + "px"; tip.style.display = "block";
}
function onLeave() {
  var t = el("tip"); if (t) t.style.display = "none";
  var h = el("hair"); if (h) h.setAttribute("visibility", "hidden");
}

(function init() {
  var wdiv = el("windows");
  Object.keys(WINDOWS).forEach(function (name) {
    var b = document.createElement("button");
    b.textContent = name; b.setAttribute("data-win", name);
    if (name === activeWindow) b.className = "active";
    b.onclick = function () { setWindow(name); };
    wdiv.appendChild(b);
  });
  el("caption").textContent = "last " + activeWindow;
  var svg = el("chart");
  svg.addEventListener("mousemove", onMove);
  svg.addEventListener("mouseleave", onLeave);

  el("sensorsel").onchange = function () {
    sensor = this.value;
    var u = new URL(location.href);
    u.searchParams.set("sensor_id", sensor);
    history.replaceState(null, "", u);
    refreshCurrent(); refreshHistory();
  };

  refreshSensors(); refreshCurrent(); refreshHistory();
  setInterval(refreshSensors, 30000);
  setInterval(refreshCurrent, 3000);
  setInterval(refreshHistory, 60000);
})();
</script>
</body>
</html>
`
