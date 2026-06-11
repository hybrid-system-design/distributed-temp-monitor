# Phase 3 — Display node (plan)

> Status: **planned, not yet built.** Decisions captured from review:
> auto-rotate through all rooms; ILI9341 320×240 bus-display board, no buttons;
> show big current temperature + room name, a 48h graph with a min–max band, and
> a stale/last-seen indicator.

## Context
Repurpose the bus-display ESP32 + ILI9341 (320×240) into a read-only wall
display: it auto-rotates through every room that has data, polling the Go API,
and renders the live value, a 48h min–max graph, and a staleness indicator. No
buttons, no publishing — it only consumes the HTTP API. The existing
`wifi` / `socketpool` / `adafruit_requests` / `displayio` scaffold is reused; the
graph (bitmap drawing) is new.

## Files
- `firmware/display/code.py` — the whole display app (CircuitPython auto-runs `code.py`).
- `firmware/display/settings.example.toml` — `CIRCUITPY_WIFI_SSID/PASSWORD`,
  `TEMPMON_BASE_URL` (e.g. `http://192.168.x.x:8080`), `ROTATE_SECONDS`. Real
  `settings.toml` is gitignored.
- Server tweak (small, see below) in `server/internal/api/server.go` + store, with tests.
- README + ROADMAP updates; CI gains a CircuitPython syntax check for `code.py`.

## Config (`settings.toml`)
CircuitPython 8+ auto-connects WiFi from `CIRCUITPY_WIFI_*`. Everything else via
`os.getenv()`:
- `TEMPMON_BASE_URL` — the Go service base URL.
- `ROTATE_SECONDS` — seconds per room (default 10).

## Architecture (`code.py`)
**Networking:** `pool = socketpool.SocketPool(wifi.radio)`;
`requests = adafruit_requests.Session(pool, ssl.create_default_context())`.
Three GETs: `/api/sensors`, `/api/current?sensor_id=`,
`/api/history?sensor_id=&hours=48&bucket=600`.

**Loop (three `time.monotonic()` timers):**
- **Sensor list** every ~60 s → `GET /api/sensors` (the rooms to rotate through).
- **Rotate** every `ROTATE_SECONDS` → advance to next room, fetch current +
  history, redraw.
- **Current** every ~5 s → refresh just the live value/staleness of the room on
  screen (so it ticks between rotations).
- Empty list → show "waiting for data".

**Layout (320×240, `displayio.Group`):**
- Top row: room name (left) + staleness chip (right) — green "live" or red
  "stale 4m" from `stale` / `age_seconds`.
- Big temp label (`terminalio.FONT`, scale ~6): `21.5°C`.
- Bottom ~120 px: graph in a `displayio.Bitmap` + `TileGrid` (3-colour palette:
  bg / band / line). Per bucket draw the min–max vertical span (dim) and the avg
  pixel (bright); connect avg between adjacent non-gap buckets. Missing buckets →
  empty columns = visible gaps, consistent with the web. Min/max temperature
  labels at the graph edges.

**Graph scaling:** map each bucket to an X column by time (linear, matching the
web), Y by value across the data's min..max. Reuse the web's gap rule (gap if the
step > ~1.5× bucket).

## Small server addition (enables clean device code)
Parsing RFC3339 on CircuitPython is painful, and the store already holds bucket
times as unix seconds. Add an integer **`ts`** to each `/api/history` point (and
`from_ts` / `to_ts` on the envelope) alongside the existing RFC3339 strings —
backward-compatible, the web ignores it. The device then does pure integer time
math. Covered by a `server_test.go` assertion.

> Open decision: confirm adding `ts`. Alternative is parsing RFC3339 on-device
> (more fragile). Recommended: add `ts`.

## Verification
- **Automatable (CI / CPython):** factor the pure helpers — graph X/Y scaling,
  gap detection, stale-text formatting — so they run under desktop Python; add a
  `firmware/display/` logic test (like the old `test.py`) that hits the live API
  and checks parse + scaling. Add a `py_compile` check for `code.py` to the CI
  firmware job.
- **On-device (manual):** copy `code.py` + Adafruit libs (`adafruit_requests`,
  `adafruit_ili9341`, `adafruit_display_text`; `displayio`/`terminalio`/`wifi`
  are built-in) via the **web editor** (this board isn't a USB drive), set
  `settings.toml`, confirm it rotates rooms and shows temp + graph + stale chip.
  Easiest end-to-end check: run the simulator to seed a few rooms, then watch
  them rotate.

## Risks / notes
- RAM: 288 history points + a 320-wide bitmap is fine on ESP32, but keep one
  history set in memory at a time and free the old group on redraw.
- No buttons → rotation is purely time-based; nothing to debounce.
- The old `bustime-display-main/` stays as-is (historical), superseded by
  `firmware/display/` — same pattern as the Brewfather sketch.

## Out of scope
Touchscreen / room pinning, per-room colour themes, on-device config UI.
