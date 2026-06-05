# Testing

The test strategy is layered by the question each layer answers for someone
evaluating this as a condition-monitoring product: *does the logic hold?*, *does
the pipeline actually work end-to-end?*, and *does it survive a flaky sensor or a
dropped network?* CI runs every layer on each push and pull request.

## Layers

| Layer | Where | What it proves | Runtime |
|-------|-------|----------------|---------|
| **Unit** | `server/internal/**/*_test.go` | Pure logic: SQLite round-trip & bucketing math, `/api/current` 404 + stale-flag, the burst-tiebreak rule, the timestamp sanity window, topic parsing. | ~1 s |
| **Fuzz** | `server/internal/ingest/fuzz_test.go` | The MQTT wire-format parser never panics and never accepts a sample that violates storage invariants, for *arbitrary* bytes. | seconds–∞ |
| **Integration** | `server/internal/integration/` (build tag `integration`) | The real seam against a real **Mosquitto** container: publish → subscribe → SQLite → HTTP, plus downsampling, staleness, **bad-payload survival**, and **reconnect after a broker drop**. | ~10 s |
| **Container smoke** | CI `docker` job | The shipped `scratch` image builds and serves `/healthz` via `docker compose`. | ~30 s |
| **Firmware build** | CI `firmware` job | The Arduino sketch compiles against the ESP32 core; the CircuitPython sources pass a syntax check. No hardware. | ~minutes |

## Running locally

```bash
cd server

# Unit tests (fast, no Docker). Add -race on Linux/macOS (needs a C toolchain).
go test ./...
go test -race ./...

# Fuzz the payload parser (Ctrl-C to stop; or use -fuzztime).
go test ./internal/ingest/ -run x -fuzz FuzzParseSample -fuzztime 30s

# Integration tests — requires Docker running (pulls eclipse-mosquitto:2).
go test -tags integration -count=1 ./internal/integration/...

# Static analysis (what CI runs).
gofmt -l .                       # must print nothing
go vet ./...
go install honnef.co/go/tools/cmd/staticcheck@latest && staticcheck ./...
```

The simulator (`tools/simulator`) doubles as an ad-hoc load generator — e.g.
`go run . --broker tcp://localhost:1883 --sensor-id load --live --step 50ms`.

## Notes

- **Reconnect testing** drives the service through a tiny in-process TCP proxy
  (`integration_test.go`); stopping and restarting the proxy severs the broker
  link without bouncing the container, exercising paho's auto-reconnect
  deterministically. Reconnect intervals are configurable
  (`MQTT_CONNECT_RETRY_INTERVAL`, `MQTT_MAX_RECONNECT_INTERVAL`) so the test
  converges in ~1 s.
- **`testcontainers-go` is a test-only dependency.** It never links into the
  `./cmd/tempmon` binary — the shipped `scratch` image still contains only the
  static server plus its two runtime deps (`paho.mqtt.golang`, `modernc.org/sqlite`).

## Deliberately out of scope

Restraint is part of the design. The following are intentionally **not** here at
this scale (single producer, single consumer, SQLite):

- Coverage *gates* — coverage is reported, not used as a pass/fail threshold
  (threshold-chasing produces hollow tests).
- Mutation testing and consumer-contract frameworks (e.g. Pact) — the README
  contract plus the integration test already pin the seam.
- A standing load/soak harness — the simulator covers ad-hoc load on demand.
- Hardware-in-the-loop CI and end-to-end UI tests.
