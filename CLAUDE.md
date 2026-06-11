# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working preferences

- **Do not create or suggest pull requests on your own.** After finishing a piece of work, present it and let the user check/verify it first. Only open a PR (or even propose opening one) when the user explicitly asks. The same goes for pushing and merging — wait for the user's go-ahead.

## Repository Overview

This is a portfolio repository containing two independent embedded systems / IoT projects for ESP32-class microcontrollers, developed for Hybrid System Design.

---

## Project 1: Bus Time Display (`bustime-display-main/`)

**Platform:** CircuitPython on ESP microcontroller with ILI9341 TFT display (320×240)

**Purpose:** Real-time public transport departure display for Trondheim, Norway, pulling data from the Entur GraphQL API.

### How to run / deploy
No build step — CircuitPython is interpreted at runtime.
1. Set WiFi credentials in `settings.toml` (`CIRCUITPY_WIFI_SSID` / `CIRCUITPY_WIFI_PASSWORD`); CircuitPython auto-connects from it. `settings.toml` is gitignored.
2. Upload `esp.py` to the microcontroller via the CircuitPython **web editor** (the device does not appear as a USB drive).
3. The device auto-runs `code.py` or `main.py` on boot; rename as needed for the target runtime.

Use `test.py` to exercise the Entur API from a regular Python environment (no hardware required):
```
python test.py
```

### Architecture
- **WiFi + NTP** → time is set to UTC+2 (Norway).
- **Entur GraphQL API** (`https://api.entur.io/journey-planner/v3/graphql`) queried every 20 s for departures at two stop IDs: `NSR:StopPlace:43666` (Berg studentby) and `NSR:StopPlace:43507` (Dybdahls veg).
- **Display loop** updates the clock every 1 s and bus data every 20 s; renders three columns ("Berg opp", "Berg ned", "Dybdahls").
- Departure times render as `Xmin` if < 20 min away, otherwise `HH:MM`.
- Custom direction-reversal logic for line 14; bus FB73 is filtered out.

---

## Project 2: ESP Brewfather Temperature Controller (`esp_brewfather_connect_temp_kontroller-hsd-portfolio/`)

**Platform:** Arduino (C++) on ESP32 DoitESP32devkitV1

**Purpose:** Fermentation temperature controller with local OLED display, relay-driven fridge control, and a built-in web dashboard.

### How to build & upload
Configured for **Arduino IDE** or `arduino-cli`:
```
arduino-cli compile --fqbn esp32:esp32:esp32doit-devkit-v1 esp_brewfather_connect_temp_kontroller.ino
arduino-cli upload  --fqbn esp32:esp32:esp32doit-devkit-v1 --port COM4 esp_brewfather_connect_temp_kontroller.ino
```
Serial monitor at 115200 baud prints the device IP after WiFi connects.

VSCode settings live in `.vscode/arduino.json` (port: COM4) and `.vscode/c_cpp_properties.json` (ESP32 toolchain paths).

Before flashing, copy `secrets.example.h` to `secrets.h` and set `WIFI_SSID` / `WIFI_PASSWORD`. `secrets.h` is gitignored and must never be committed.

### Architecture
The sketch runs two FreeRTOS tasks on separate cores:

| Task | Core | Responsibility |
|------|------|----------------|
| Temperature task | 0 | Read MAX6675 thermocouple via SPI every 1 s, average N=5 samples, record 15-min history (96-entry circular buffer), drive fridge relay |
| Web task | 1 | Serve HTTP on port 80 |

**HTTP endpoints:**
- `GET /` — HTML dashboard with Chart.js temperature graph
- `GET /data` — JSON: `{ temp, set_temp, fridge_state }`
- `GET /history` — JSON: 24-hour temperature history with NTP timestamps

**Hardware pins:**
- MAX6675 SPI: SCK=18, CS=19, SO=5
- Fridge relay: GPIO 32 (on) / 33 (off)
- Buttons (ISR): GPIO 25 (temp up), GPIO 26 (temp down)
- SSD1306 OLED: I²C default pins

**Control logic:** fridge relay ON when `avg_temp > set_temp`, OFF when `avg_temp ≤ set_temp`. Boiling-point auto-calibration applies a −3 °C offset.

**NTP:** `pool.ntp.org`, timezone CET/CEST. History entries are only recorded after a valid NTP timestamp is obtained.

> **Note:** Brewfather API integration code exists but is commented out — the stream upload endpoint was planned but not completed.
