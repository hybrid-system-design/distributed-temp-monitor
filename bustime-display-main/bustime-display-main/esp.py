import adafruit_requests
import wifi
import socketpool
import ssl
import json
import board
import time
import adafruit_ntp
import rtc
import displayio
import terminalio
import adafruit_ili9341
from adafruit_display_text import label

# --- Configuration & Constants ---
api_url = 'https://api.entur.io/journey-planner/v3/graphql'
headers = {
    'Content-Type': 'application/json',
    'ET-Client-Name': 'janschop-github'
}

# The GraphQL query as a Python dictionary (cleaner than a raw string)
graphql_query = {
    "query": "{ stopPlaces(ids: [\"NSR:StopPlace:43666\", \"NSR:StopPlace:43507\"]) { name estimatedCalls(timeRange: 3600, numberOfDepartures: 20) { cancellation realtime expectedDepartureTime destinationDisplay { frontText } serviceJourney { journeyPattern { directionType line { id name } } } } } }"
}

threshold_time_to_minutes = 20
font = terminalio.FONT
text_color = 0xFFFFFF
title_color = 0x79b6c9
font_scale = 2

# --- UI Setup ---
displayio.release_displays()

text_area_clock = label.Label(font, color=0xFFFFFF, scale=6, x=15, y=30)
text_area_title_1 = label.Label(font, color=title_color, scale=font_scale, x=0, y=70, text="Berg opp")
text_area_title_2 = label.Label(font, color=title_color, scale=font_scale, x=106, y=70, text="Berg ned")
text_area_title_3 = label.Label(font, color=title_color, scale=font_scale, x=212, y=70, text="Dybdahls")
text_area_1 = label.Label(font, color=text_color, scale=font_scale, x=0, y=100)
text_area_2 = label.Label(font, color=text_color, scale=font_scale, x=106, y=100)
text_area_3 = label.Label(font, color=text_color, scale=font_scale, x=212, y=100)

group = displayio.Group()
for area in [text_area_clock, text_area_1, text_area_2, text_area_3, 
             text_area_title_1, text_area_title_2, text_area_title_3]:
    group.append(area)

spi = board.SPI()
tft_cs = board.D5
tft_dc = board.D4
display_bus = displayio.FourWire(spi, command=tft_dc, chip_select=tft_cs)
display = adafruit_ili9341.ILI9341(display_bus, width=320, height=240)
display.root_group = group

# --- Helper Functions ---
def parse_bus_number(bus_id):
    if bus_id.startswith('UNI:Line:'):
        return bus_id.split(':')[-1]
    elif bus_id.startswith('ATB:Line:'):
        return bus_id.split('_')[-1]
    return bus_id

def reverse_direction(direction):
    return 'outbound' if direction == 'inbound' else 'inbound'

def clean_data(call, stop_name):
    bus_number = parse_bus_number(call['serviceJourney']['journeyPattern']['line']['id'])
    destination = call['destinationDisplay']['frontText']
    departure_time = call['expectedDepartureTime'].split('T')[1][:8]
    direction = call['serviceJourney']['journeyPattern']['directionType']

    if bus_number == '14':
        direction = reverse_direction(direction)

    return {
        'bus_stop': stop_name,
        'cancelled': call['cancellation'],
        'departure_time': departure_time,
        'bus_number': bus_number,
        'destination': destination,
        'direction': direction,
    }

def handle_response(response):
    if response.status_code != 200:
        print('API Error:', response.status_code)
        return [], [], []
    
    berg_outbound, berg_inbound, dybdahls_inbound = [], [], []
    data = response.json()

    for stop in data.get('data', {}).get('stopPlaces', []):
        stop_name = stop.get('name', '')
        for call in stop.get('estimatedCalls', []):
            cleaned = clean_data(call, stop_name)
            if stop_name == 'Dybdahls veg':
                if not cleaned['cancelled'] and cleaned['direction'] != 'outbound':
                    dybdahls_inbound.append(cleaned)
            elif stop_name == 'Berg studentby':
                if cleaned['bus_number'] != 'FB73' and not cleaned['cancelled']:
                    if cleaned['direction'] == 'outbound':
                        berg_outbound.append(cleaned)
                    else:
                        berg_inbound.append(cleaned)
    return berg_outbound, berg_inbound, dybdahls_inbound

def is_less_than_X_minutes_away(given_time, threshold):
    now = time.localtime()
    current_minutes = now.tm_min + 60 * now.tm_hour
    given_minutes = int(given_time[3:5]) + 60 * int(given_time[0:2])
    diff = given_minutes - current_minutes

    if 0 <= diff <= 1:
        return 'now'
    elif 0 < diff < threshold:
        return diff
    return False

def format_bus_text(buses):
    lines = []
    for bus in buses:
        ret = is_less_than_X_minutes_away(bus['departure_time'], threshold_time_to_minutes)
        time_str = f"{ret}min" if isinstance(ret, int) else (ret if ret == 'now' else bus['departure_time'][0:5])
        lines.append(f"{bus['bus_number']} {time_str}")
    return "\n".join(lines)

# --- Initialization ---
print("Connecting to WiFi...")
pool = socketpool.SocketPool(wifi.radio)
requests = adafruit_requests.Session(pool, ssl.create_default_context())

print("Syncing Time...")
ntp = adafruit_ntp.NTP(pool, tz_offset=2)
rtc.RTC().datetime = ntp.datetime

# --- Main Loop ---
bus_timer = 0
clock_timer = time.monotonic()

while True:
    try:
        # Update clock every second from internal RTC
        if time.monotonic() - clock_timer >= 1.0:
            t = time.localtime()
            text_area_clock.text = f"{t.tm_hour:02}:{t.tm_min:02}:{t.tm_sec:02}"
            clock_timer = time.monotonic()

        # Update bus data every 20 seconds
        if time.monotonic() - bus_timer >= 20.0:
            bus_timer = time.monotonic()
            print("Fetching Entur data...")
            with requests.post(api_url, headers=headers, json=graphql_query) as response:
                b_out, b_in, d_in = handle_response(response)
                text_area_1.text = format_bus_text(b_out)
                text_area_2.text = format_bus_text(b_in)
                text_area_3.text = format_bus_text(d_in)
            
    except Exception as e:
        print("Error:", e)
        time.sleep(5) # Wait before retrying on crash