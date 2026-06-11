# Temperature display node for the distributed temperature monitor.
#
# Read-only wall display on an ESP32 + ILI9341 (320x240). It auto-rotates through
# every room that has data (from /api/sensors) and, for the room on screen, shows
# the big live value, a stale/last-seen chip, and a 48h min-max graph drawn on a
# linear time axis (missing buckets show as gaps). No buttons, no publishing.
#
# Config lives in settings.toml (CircuitPython auto-connects WiFi from
# CIRCUITPY_WIFI_SSID/PASSWORD):
#   TEMPMON_BASE_URL = "http://192.168.1.50:8080"
#   ROTATE_SECONDS   = "10"
#
# Device libraries needed (copy from the Adafruit CircuitPython bundle):
#   adafruit_requests, adafruit_ili9341, adafruit_display_text
# Built-in: wifi, socketpool, ssl, displayio, terminalio, vectorio, os, time.
# Also copy graphutil.py alongside this file.

import os
import time
import ssl

import wifi
import socketpool
import board
import displayio
import terminalio
import vectorio
import adafruit_requests
import adafruit_ili9341
from adafruit_display_text import label

import graphutil

try:
    from fourwire import FourWire  # CircuitPython 9+
except ImportError:
    from displayio import FourWire  # older builds

BASE_URL = os.getenv("TEMPMON_BASE_URL") or ""
ROTATE_SECONDS = int(os.getenv("ROTATE_SECONDS") or 10)
UTC_OFFSET_HOURS = int(os.getenv("UTC_OFFSET_HOURS") or 0)
CURRENT_SECONDS = 5    # refresh the live value (cheap: temp label only)
HISTORY_SECONDS = 60   # redraw the graph for the current room
SENSORS_SECONDS = 60   # refresh the room list
HISTORY_HOURS = 48
HISTORY_BUCKET = 600

# Graph geometry (within the 320x240 screen).
GX, GY = 40, 128
GW, GH = 268, 96

# Colours.
WHITE = 0xFFFFFF
DIM = 0x8B97A4
GREEN = 0x3FB950
DIMGREEN = 0x1F5F2F
RED = 0xF85149
GRID = 0x2B3640

# Graph axis divisions (gridlines = divisions + 1).
NY = 4  # horizontal lines / temperature labels
NX = 4  # vertical lines / time labels

# --- display setup ---------------------------------------------------------
displayio.release_displays()
spi = board.SPI()
tft_cs = board.D5  # chip select   } exact wiring from the original bus display
tft_dc = board.D4  # data/command  } (esp.py); no reset pin
display_bus = FourWire(spi, command=tft_dc, chip_select=tft_cs)
display = adafruit_ili9341.ILI9341(display_bus, width=320, height=240)

splash = displayio.Group()
display.root_group = splash

# Black background.
bg_palette = displayio.Palette(1)
bg_palette[0] = 0x000000
splash.append(vectorio.Rectangle(pixel_shader=bg_palette, width=320, height=240, x=0, y=0))

room_lbl = label.Label(terminalio.FONT, text="", color=WHITE, scale=3, x=8, y=18)
stale_lbl = label.Label(terminalio.FONT, text="", color=0x000000, scale=1, x=210, y=14,
                        background_color=DIMGREEN, padding_left=4, padding_right=4,
                        padding_top=2, padding_bottom=2)
temp_lbl = label.Label(terminalio.FONT, text="--", color=WHITE, scale=5, x=20, y=72)
for w in (room_lbl, stale_lbl, temp_lbl):
    splash.append(w)

# Axis tick labels: temperatures down the Y axis, times along the X axis.
y_labels = []
for _ in range(NY + 1):
    lb = label.Label(terminalio.FONT, text="", color=DIM, scale=1)
    lb.anchor_point = (1.0, 0.5)  # right-aligned, just left of the graph
    y_labels.append(lb)
    splash.append(lb)
x_labels = []
for _ in range(NX + 1):
    lb = label.Label(terminalio.FONT, text="", color=DIM, scale=1)
    lb.anchor_point = (0.5, 0.0)  # centred under each vertical line
    x_labels.append(lb)
    splash.append(lb)

# Graph bitmap: 0 = background, 1 = min-max band, 2 = average line.
graph_palette = displayio.Palette(4)
graph_palette[0] = 0x000000
graph_palette[1] = DIMGREEN
graph_palette[2] = GREEN
graph_palette[3] = GRID
graph_bmp = displayio.Bitmap(GW, GH, 4)
splash.append(displayio.TileGrid(graph_bmp, pixel_shader=graph_palette, x=GX, y=GY))

# --- networking ------------------------------------------------------------
pool = socketpool.SocketPool(wifi.radio)
requests = adafruit_requests.Session(pool, ssl.create_default_context())


def get_json(path):
    """GET BASE_URL+path -> parsed JSON, or None on any error/non-200."""
    try:
        resp = requests.get(BASE_URL + path)
    except Exception as e:  # noqa: BLE001 - network errors must not crash the loop
        print("request error:", e)
        return None
    try:
        if resp.status_code != 200:
            return None
        return resp.json()
    finally:
        resp.close()


# --- drawing ---------------------------------------------------------------
def _line(x0, y0, x1, y1):
    # Bresenham line in the avg colour.
    dx = abs(x1 - x0)
    dy = -abs(y1 - y0)
    sx = 1 if x0 < x1 else -1
    sy = 1 if y0 < y1 else -1
    err = dx + dy
    while True:
        if 0 <= x0 < GW and 0 <= y0 < GH:
            graph_bmp[x0, y0] = 2
        if x0 == x1 and y0 == y1:
            break
        e2 = 2 * err
        if e2 >= dy:
            err += dy
            x0 += sx
        if e2 <= dx:
            err += dx
            y0 += sy


def _hline(y):
    if 0 <= y < GH:
        for x in range(GW):
            graph_bmp[x, y] = 3


def _vline(x):
    if 0 <= x < GW:
        for y in range(GH):
            graph_bmp[x, y] = 3


def _clear_axis_labels():
    for lb in y_labels:
        lb.text = ""
    for lb in x_labels:
        lb.text = ""


def draw_graph(hist):
    graph_bmp.fill(0)
    points = hist.get("points") if hist else None
    if not points:
        _clear_axis_labels()
        return
    from_ts = hist["from_ts"]
    to_ts = hist["to_ts"]
    bucket = hist.get("bucket_seconds", HISTORY_BUCKET)
    lo, hi = graphutil.value_range(points)

    # Y axis: horizontal gridlines + temperature labels (top = hi).
    for g in range(NY + 1):
        y = int(g * (GH - 1) / NY)
        _hline(y)
        y_labels[g].text = "%.1f" % (hi - (hi - lo) * g / NY)
        y_labels[g].anchored_position = (GX - 3, GY + y)

    # X axis: vertical gridlines + time labels (linear in time).
    for g in range(NX + 1):
        x = int(g * (GW - 1) / NX)
        _vline(x)
        ts = int(from_ts + (to_ts - from_ts) * g / NX)
        lb = x_labels[g]
        lb.text = graphutil.hhmm(ts, UTC_OFFSET_HOURS)
        lb.anchor_point = (0.0, 0.0) if g == 0 else ((1.0, 0.0) if g == NX else (0.5, 0.0))
        lb.anchored_position = (GX + x, GY + GH + 4)

    # Data on top of the grid: min-max band + average line.
    prev_ts = None
    prev_x = 0
    prev_y = 0
    for p in points:
        x = graphutil.x_for(p["ts"], from_ts, to_ts, GW)
        ymin = graphutil.y_for(p["min"], lo, hi, GH)
        ymax = graphutil.y_for(p["max"], lo, hi, GH)
        yavg = graphutil.y_for(p["avg"], lo, hi, GH)
        top = ymax if ymax < ymin else ymin
        bot = ymin if ymin > ymax else ymax
        for y in range(top, bot + 1):
            graph_bmp[x, y] = 1
        if prev_ts is not None and not graphutil.is_gap(prev_ts, p["ts"], bucket):
            _line(prev_x, prev_y, x, yavg)
        graph_bmp[x, yavg] = 2
        prev_ts, prev_x, prev_y = p["ts"], x, yavg


def show_current(cur):
    if not cur:
        temp_lbl.text = "--"
        stale_lbl.text = "no data"
        stale_lbl.background_color = RED
        return
    temp_lbl.text = "%.1f C" % cur["value"]
    if cur.get("stale"):
        stale_lbl.text = "stale " + graphutil.format_age(cur.get("age_seconds", 0))
        stale_lbl.background_color = RED
    else:
        stale_lbl.text = "live"
        stale_lbl.background_color = DIMGREEN


def show_message(msg):
    room_lbl.text = msg
    temp_lbl.text = "--"
    stale_lbl.text = ""
    graph_bmp.fill(0)
    _clear_axis_labels()


def refresh_value(room):
    show_current(get_json("/api/current?sensor_id=" + room))


def refresh_graph(room):
    draw_graph(get_json(
        "/api/history?sensor_id=" + room
        + "&hours=" + str(HISTORY_HOURS) + "&bucket=" + str(HISTORY_BUCKET)))


# --- main loop -------------------------------------------------------------
def main():
    if not BASE_URL:
        show_message("set TEMPMON_BASE_URL")
        while True:
            time.sleep(5)

    # WiFi auto-connects from settings.toml; give it a moment if needed.
    for _ in range(20):
        if wifi.radio.connected:
            break
        time.sleep(0.5)

    sensors = []
    idx = 0
    shown = None  # the room currently drawn on screen
    now = time.monotonic()
    t_sensors = -SENSORS_SECONDS  # force an immediate fetch
    t_rotate = now
    t_current = now
    t_history = now

    while True:
        now = time.monotonic()

        if now - t_sensors >= SENSORS_SECONDS:
            t_sensors = now
            data = get_json("/api/sensors")
            if data is not None:
                sensors = data.get("sensors", [])
                if idx >= len(sensors):
                    idx = 0

        if not sensors:
            show_message("waiting for data")
            shown = None
            time.sleep(2)
            continue

        # Rotate only when there's more than one room to show.
        if len(sensors) > 1 and now - t_rotate >= ROTATE_SECONDS:
            t_rotate = now
            idx = (idx + 1) % len(sensors)

        room = sensors[idx]

        if room != shown:
            # New room (rotation or first display): full refresh.
            shown = room
            room_lbl.text = room
            refresh_value(room)
            refresh_graph(room)
            t_current = now
            t_history = now
        else:
            # Same room on screen: cheap value tick, slow graph redraw.
            if now - t_current >= CURRENT_SECONDS:
                t_current = now
                refresh_value(room)
            if now - t_history >= HISTORY_SECONDS:
                t_history = now
                refresh_graph(room)

        time.sleep(0.2)


main()
