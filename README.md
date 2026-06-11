# Temperature Monitor & Display System

[![CI](https://github.com/hybrid-system-design/distributed-temp-monitor/actions/workflows/ci.yml/badge.svg)](https://github.com/hybrid-system-design/distributed-temp-monitor/actions/workflows/ci.yml)

A distributed temperature-monitoring system: an ESP32 sensor node publishes
readings over MQTT; a small Go service ingests them, persists to SQLite,
downsamples server-side, and serves HTTP; an ESP32 display node polls HTTP and
shows the live value plus a 48-hour graph with a staleness indicator.

Built as an HSD portfolio piece — the shape of a real condition-monitoring
deployment, deliberately scoped: SQLite (not Influx/Timestream), a single static
binary, one MQTT topic, metric units throughout.

```
ESP32 (Arduino, sensor)  --MQTT-->  Broker + Go service  --HTTP-->  ESP32 (CircuitPython, display)
                                          |
                                     SQLite (time-series)
```

## Repository layout

| Path | What it is |
|------|------------|
| `server/` | Go service: MQTT subscriber → SQLite → HTTP API + web dashboard at `/` |
| `tools/simulator/` | Replay/simulator that publishes a canned 48h series to the broker |
| `deploy/` | `docker-compose.yml` (broker + service + volume) and `mosquitto.conf` |
| `firmware/sensor/` | Sensor node firmware (Arduino): reads MAX6675, publishes over MQTT |
| `bustime-display-main/` | Display node firmware (CircuitPython) — repurposed in Phase 3 |
| `esp_brewfather_connect_temp_kontroller-hsd-portfolio/` | Original Brewfather controller — superseded by `firmware/sensor/` |

> **Status:** Phase 1 (server + infra) and Phase 2 (sensor firmware + web UI) are
> implemented. The ESP32 display node is Phase 3 (see `ROADMAP.md`). The data
> contract is fixed so each node has a stable seam to target.

## One-command bring-up

```bash
cd deploy
docker compose up -d --build
```

This starts Mosquitto (`:1883`) and the Go service (`:8080`). Check it:

```bash
curl localhost:8080/healthz            # {"ok":true}
docker compose logs -f tempmon         # watch ingest
docker compose down                    # stop (add -v to wipe the DB volume)
```

Then open **http://localhost:8080/** in a browser for the live dashboard (current
value, selectable-window chart with hover tooltips, staleness badge). Append
`?sensor_id=<id>` to pick a sensor.

The DB lives in the named volume `tempdb` (`/data/tempmon.db` in the container),
so data survives restarts. Both services use `restart: unless-stopped`, so the
stack comes back on reboot — identical on an always-on host or a demo laptop.

## Demo without hardware (simulator)

Backfill 48h of realistic history, then optionally stream live samples. The
simulator publishes under its own sensor id (default `sim`) so demo data never
mixes with a real sensor like `fermenter-1` — view it at `/?sensor_id=sim`.

```bash
cd tools/simulator
go run . --broker tcp://localhost:1883            # backfill as sensor "sim"
go run . --broker tcp://localhost:1883 --live     # backfill + live
```

Flags: `--hours` (default 48), `--step` (default `5m`), `--setpoint` (°C),
`--amplitude` (diurnal swing °C), `--live`.

## Data contract — the electronics/IT seam

### MQTT

- **Broker:** Mosquitto, TCP `1883`, anonymous (trusted LAN / demo only).
- **Topic:** `sensors/<sensor_id>/temperature` (the server subscribes to
  `sensors/+/temperature`).
- **Payload** (JSON, QoS 1, not retained):

  ```json
  { "value": 19.4, "unit": "C", "timestamp": "2026-06-03T12:00:00Z", "sensor_id": "fermenter-1" }
  ```

  | Field | Type | Notes |
  |-------|------|-------|
  | `value` | number | temperature in °C (required) |
  | `unit` | string | always `"C"` (defaults to `C` if omitted) |
  | `timestamp` | string | RFC3339 UTC (optional; see timestamp rule) |
  | `sensor_id` | string | required; falls back to the topic segment if omitted |

  Payloads with no `value` or no resolvable `sensor_id` are logged and dropped —
  ingestion never crashes on bad input.

### Timestamp rule

The server stores two times per sample:

- `received_at` — server wall-clock at receipt; **always real**, drives staleness.
- `event_time` — canonical series time. Equals the payload `timestamp` **if** it
  lands within `[now − 50h, now + 5min]`, otherwise falls back to `received_at`.

This honors a replay of the last 48 hours (so the simulator backfills a real
graph) while rejecting nonsense from a sensor with an unsynced clock (1970,
far-future) by using arrival time instead.

### HTTP API

All responses are JSON with permissive CORS (`Access-Control-Allow-Origin: *`)
so the display can fetch them directly.

**`GET /api/current?sensor_id=<id>`** — latest reading (by arrival time):

```json
{
  "sensor_id": "fermenter-1",
  "value": 19.4,
  "unit": "C",
  "event_time": "2026-06-03T12:00:00Z",
  "received_at": "2026-06-03T12:00:03Z",
  "age_seconds": 3,
  "stale": false
}
```

`stale` is `true` when `age_seconds` exceeds `STALE_THRESHOLD` (default 120s).
Returns `404` if the sensor has never reported.

**`GET /api/history?sensor_id=<id>&hours=48&bucket=600`** — server-side
downsampled series (averaged into fixed time buckets; ≤288 points at the
defaults, comfortably under the 320px display width):

```json
{
  "sensor_id": "fermenter-1",
  "unit": "C",
  "bucket_seconds": 600,
  "from": "2026-06-01T12:00:00Z",
  "to": "2026-06-03T12:00:00Z",
  "points": [
    { "t": "2026-06-01T12:00:00Z", "avg": 19.4, "min": 19.1, "max": 19.6, "n": 5 }
  ]
}
```

Query params: `hours` (default 48, clamped ≤720), `bucket` seconds (default 600).

**`GET /api/sensors`** — distinct sensor ids that have reported, sorted:
`{"sensors":["fermenter-1","gutterom","soverom","stue"]}`. Powers the dashboard
dropdown.

**`GET /healthz`** — `{"ok":true}`.

**`GET /`** — self-contained interactive web dashboard: live value + stale badge,
a chart with a selectable time window (24h / 48h / 7 days), time-axis labels, a
hover tooltip (avg/min/max per bucket), a shaded min–max band, and a sensor/room
dropdown (from `/api/sensors`). The X-axis is linear in time, and missing data
shows as real gaps (the line breaks rather than interpolating across). Polls the
endpoints above; no external assets, so it
works offline.

## Configuration (server env vars)

| Var | Default | Meaning |
|-----|---------|---------|
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `MQTT_URL` | `tcp://localhost:1883` | broker URL |
| `MQTT_TOPIC` | `sensors/+/temperature` | subscription filter |
| `MQTT_CLIENT_ID` | `tempmon-server` | MQTT client id |
| `DB_PATH` | `tempmon.db` | SQLite file path |
| `STALE_THRESHOLD` | `120s` | age beyond which `/api/current` is `stale` |
| `SANITY_PAST` / `SANITY_FUTURE` | `50h` / `5m` | timestamp acceptance window |

## Local development (without Docker)

```bash
cd server
go test ./...
go run ./cmd/tempmon        # needs a broker at MQTT_URL
```

Dependencies (`modernc.org/sqlite` pure-Go driver, `eclipse/paho.mqtt.golang`)
are pinned in `go.mod` with checksums in the committed `go.sum`. Run
`go mod tidy` only after changing imports.

## Testing

Unit + fuzz + a real-broker integration suite (testcontainers) + container and
firmware build checks, all run in CI on every push/PR. See **[TESTING.md](TESTING.md)**
for the layered strategy and how to run each locally. Quick start:

```bash
cd server
go test ./...                                          # unit (fast, no Docker)
go test -tags integration -count=1 ./internal/integration/...   # needs Docker
```

## Sensor firmware (`firmware/sensor/`)

A dumb Arduino sensor node: reads the MAX6675 thermocouple (reported as a moving
average of the last 5 readings, since it's noisy), shows `T: <temp> C` + the
selected room on a local SSD1306 OLED, and publishes the MQTT payload above to
`sensors/<room>/temperature` every 5 s, reconnecting WiFi and MQTT as needed.
No relay/clock — the server stamps arrival time. The OLED is optional.

**Room selection.** The unit is moved between rooms; a button (GPIO 25) cycles
the room name (`soverom` → `jenterom` → `gutterom` → `stue` → …). On power-up
**no room is selected and nothing is published** until you press the button to
pick one — so a reading is never logged under the wrong room. After a switch the
first send waits ~5 s, letting the probe settle in the new room. No persistence:
after a power cycle you select the room again.

**Pause.** A second button (GPIO 26) toggles publishing on/off — readings still
update on the OLED, but nothing is sent (the OLED shows `PAUSED`). Press again to
resume.

```
cp firmware/sensor/secrets.example.h firmware/sensor/secrets.h   # set WiFi + broker
arduino-cli compile --fqbn esp32:esp32:esp32doit-devkit-v1 firmware/sensor
arduino-cli upload  --fqbn esp32:esp32:esp32doit-devkit-v1 --port COM4 firmware/sensor
```

Requires the `PubSubClient`, `Adafruit GFX`, and `Adafruit SSD1306` libraries.
CI compiles this sketch on every push.

## Phase 3 — display node (not yet implemented)

Repurpose the CircuitPython display (`bustime-display-main/`): replace the Entur
GraphQL polling with `GET /api/current` (~1s) and `GET /api/history` (~60s); reuse
the existing `adafruit_requests` + `displayio` scaffold; render the live value,
the 48h graph, and a "last seen" staleness banner when `stale` is true. See
`ROADMAP.md`.
