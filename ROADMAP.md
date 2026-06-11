# Roadmap

High-level architecture checklist. Keep it simple — one line per item, tick when done.

```
ESP32 sensor (Arduino) --MQTT--> Broker + Go service --HTTP--> ESP32 display (CircuitPython)
                                       |
                                  SQLite (48h history)
```

## Phase 1 — Server & infra ✅
- [x] Go service: MQTT subscribe → SQLite → HTTP
- [x] `/api/current` + `/api/history` (server-side downsampling)
- [x] Docker Compose stack (Mosquitto + service + volume)
- [x] Replay simulator (canned 48h, no hardware needed)
- [x] Documented data contract (MQTT payload + HTTP shapes)

## Quality — Testing & CI ✅
- [x] Unit + fuzz tests
- [x] Integration test (real broker via testcontainers)
- [x] GitHub Actions CI (lint, race, integration, docker, firmware build)

## Phase 2 — Sensor node + web UI (next)
- [x] Sensor (Arduino): strip controller code, publish temperature over MQTT
- [x] Sensor: moving-average smoothing (noisy thermocouple)
- [x] Sensor: local OLED readout (`T: <temp> C`)
- [x] Sensor: button-cycled room selection (soverom/jenterom/gutterom/stue)
- [x] Sensor: reconnect cleanly on WiFi/broker drop
- [x] Web UI: self-contained dashboard — live value, interactive chart (window selector, time axis, hover, gaps), room dropdown
- [ ] End-to-end: real sensor → broker → service → browser

## Phase 3 — Display node (ESP32 CircuitPython)
- [ ] Poll `/api/current` for the live value
- [ ] Poll `/api/history`, draw the 48h graph
- [ ] "Last seen" staleness indicator
- [ ] End-to-end test on real hardware

## Later — maybe
- [ ] Multiple sensors on one display
- [ ] MQTT auth / TLS
- [ ] History retention / compaction policy

## Principles
- SQLite, single static binary, one MQTT topic — restraint over scale.
- Metric units throughout.
- The MQTT payload + HTTP shapes are the contract; change them deliberately.
