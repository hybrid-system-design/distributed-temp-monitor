package api

// dashboardHTML is a single self-contained page served at GET /. It polls the
// JSON API and renders the live value, a 48h chart, and a staleness badge with
// no external assets (no CDN), so it works offline at demos. The chart is drawn
// as an inline SVG polyline in a few lines of vanilla JS.
//
// Note: this is a Go raw string literal, so the embedded JS must not use
// backticks (template literals) — string concatenation is used instead.
const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Temperature Monitor</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; font-family: system-ui, sans-serif; background:#11161c; color:#e6edf3; }
  .wrap { max-width:720px; margin:0 auto; padding:24px; }
  h1 { font-size:1rem; font-weight:600; color:#9aa7b4; margin:0 0 16px; }
  .val { font-size:4rem; font-weight:700; line-height:1; }
  .unit { font-size:1.5rem; color:#9aa7b4; }
  .meta { margin-top:8px; color:#9aa7b4; font-size:.9rem; }
  .badge { display:inline-block; padding:2px 10px; border-radius:999px; font-size:.8rem;
           font-weight:600; vertical-align:middle; margin-left:8px; }
  .live { background:#123d1e; color:#4ade80; }
  .stale { background:#3d1212; color:#f87171; }
  svg { width:100%; height:240px; margin-top:24px; background:#0b0f14; border-radius:8px; }
  .axis { fill:#56606b; font-size:11px; }
</style>
</head>
<body>
<div class="wrap">
  <h1>Temperature Monitor — <span id="sensor"></span></h1>
  <div><span class="val" id="value">--</span><span class="unit">&nbsp;°C</span>
       <span class="badge" id="badge">…</span></div>
  <div class="meta" id="meta">connecting…</div>
  <svg id="chart" viewBox="0 0 640 240" preserveAspectRatio="none"></svg>
  <div class="meta">48-hour history</div>
</div>
<script>
var SVGNS = "http://www.w3.org/2000/svg";
var params = new URLSearchParams(location.search);
var sensor = params.get("sensor_id") || "fermenter-1";
document.getElementById("sensor").textContent = sensor;

function el(id){ return document.getElementById(id); }

async function refreshCurrent(){
  try {
    var r = await fetch("/api/current?sensor_id=" + encodeURIComponent(sensor));
    if (r.status === 404){ el("value").textContent = "--"; el("meta").textContent = "no data yet";
      el("badge").textContent = "no data"; el("badge").className = "badge stale"; return; }
    var d = await r.json();
    el("value").textContent = d.value.toFixed(1);
    el("meta").textContent = "updated " + d.age_seconds + "s ago";
    if (d.stale){ el("badge").textContent = "STALE"; el("badge").className = "badge stale"; }
    else { el("badge").textContent = "live"; el("badge").className = "badge live"; }
  } catch(e){ el("meta").textContent = "connection error"; }
}

function clear(svg){ while(svg.firstChild) svg.removeChild(svg.firstChild); }
function addText(svg,x,y,s){
  var t = document.createElementNS(SVGNS,"text");
  t.setAttribute("x",x); t.setAttribute("y",y); t.setAttribute("class","axis");
  t.textContent = s; svg.appendChild(t);
}

function drawChart(points){
  var svg = el("chart"); clear(svg);
  if(!points || !points.length) return;
  var W=640, H=240, pad=28;
  var vals = points.map(function(p){ return p.avg; });
  var lo = Math.min.apply(null, vals), hi = Math.max.apply(null, vals);
  if (hi - lo < 1){ var m=(hi+lo)/2; lo=m-0.5; hi=m+0.5; }
  var n = points.length;
  function px(i){ return pad + i*(W-2*pad)/Math.max(1,n-1); }
  function py(v){ return H-pad - (v-lo)*(H-2*pad)/(hi-lo); }
  var pts = points.map(function(p,i){ return px(i).toFixed(1)+","+py(p.avg).toFixed(1); }).join(" ");
  var pl = document.createElementNS(SVGNS,"polyline");
  pl.setAttribute("points", pts); pl.setAttribute("fill","none");
  pl.setAttribute("stroke","#4ade80"); pl.setAttribute("stroke-width","2");
  svg.appendChild(pl);
  addText(svg, 4, py(hi)+4, hi.toFixed(1));
  addText(svg, 4, py(lo)+4, lo.toFixed(1));
}

async function refreshHistory(){
  try {
    var r = await fetch("/api/history?sensor_id=" + encodeURIComponent(sensor) + "&hours=48");
    if (!r.ok) return;
    var d = await r.json();
    drawChart(d.points || []);
  } catch(e){}
}

refreshCurrent(); refreshHistory();
setInterval(refreshCurrent, 3000);
setInterval(refreshHistory, 60000);
</script>
</body>
</html>
`
