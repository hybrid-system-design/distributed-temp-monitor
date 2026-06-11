// Temperature sensor node for the distributed temperature monitor.
//
// Deliberately dumb: read a MAX6675 thermocouple (reported as a moving average,
// since it's noisy), show it on a local OLED, and publish JSON to MQTT — no
// relay, no local web server. Graceful WiFi/MQTT reconnection. The server stamps
// arrival time, so this node carries no clock and sends no timestamp (it is
// optional in the contract).
//
// Room selection: the unit is moved between rooms. One button (GPIO 25) cycles
// the room name (soverom -> jenterom -> gutterom -> stue -> ...); on power-up no
// room is selected and NOTHING is published until you press it, so a reading is
// never logged under the wrong room. After a switch the first send waits ~5s so
// the probe can settle. A second button (GPIO 26) pauses/resumes publishing (the
// OLED shows PAUSED). The chosen room and state show on the OLED.
//
// Topic:   sensors/<room>/temperature
// Payload: {"value":21.5,"unit":"C","sensor_id":"soverom"}
//
// Libraries: PubSubClient (knolleary), Adafruit GFX + SSD1306 (OLED). WiFi/Wire
// are provided by the ESP32 core. Copy secrets.example.h to secrets.h first.

#include <WiFi.h>
#include <PubSubClient.h>
#include <Wire.h>
#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>
#include "secrets.h"

// MAX6675 thermocouple (bit-banged SPI).
#define MAX6675_SCK 18
#define MAX6675_CS  19
#define MAX6675_SO  5

// Calibration offset (°C). The reference unit read boiling water ~3 °C high.
#define TEMP_OFFSET 3.0

// Two buttons (the board's old up/down): one cycles the room, one pauses sending.
#define BTN_ROOM  25
#define BTN_PAUSE 26

// SSD1306 OLED (I2C) — optional local readout. The sensor runs fine without it.
#define SCREEN_WIDTH  128
#define SCREEN_HEIGHT 64
#define OLED_ADDR     0x3C

static const unsigned long PUBLISH_INTERVAL_MS = 5000;
static const unsigned long READ_INTERVAL_MS    = 1000;

// The thermocouple is noisy, so the reported value is a moving average of the
// last N readings. At one read per second that smooths over ~N seconds.
#define AVG_SAMPLES 5

// Rooms cycled through by the button. roomIndex == -1 means "not selected yet".
const char *ROOMS[] = { "soverom", "jenterom", "gutterom", "stue" };
const int NROOMS = 4;
volatile int roomIndex = -1;

WiFiClient net;
PubSubClient mqtt(net);
Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, -1);

char topic[64] = "";
unsigned long lastPublish = 0;
unsigned long lastRead = 0;

double sampleBuf[AVG_SAMPLES];
int sampleCount = 0;  // valid samples currently in the window (0..AVG_SAMPLES)
int sampleHead = 0;   // ring-buffer write index
double avgTemp = NAN; // current moving average (NAN until the first valid read)
bool firstReadingSkipped = false; // the MAX6675's first conversion is unreliable
bool haveDisplay = false;

bool paused = false; // when true, keep reading/displaying but stop publishing

// Button ISRs: debounced, just raise a flag handled in loop().
volatile bool roomEvent = false;
volatile unsigned long lastRoomMs = 0;
void IRAM_ATTR onRoomButton() {
  unsigned long now = millis();
  if (now - lastRoomMs > 200) { lastRoomMs = now; roomEvent = true; }
}
volatile bool pauseEvent = false;
volatile unsigned long lastPauseMs = 0;
void IRAM_ATTR onPauseButton() {
  unsigned long now = millis();
  if (now - lastPauseMs > 200) { lastPauseMs = now; pauseEvent = true; }
}

// readThermocouple returns °C, or NAN if the thermocouple is open/disconnected.
double readThermocouple() {
  pinMode(MAX6675_CS, OUTPUT);
  pinMode(MAX6675_SCK, OUTPUT);
  pinMode(MAX6675_SO, INPUT);

  digitalWrite(MAX6675_CS, LOW);
  delay(1);
  uint16_t v = shiftIn(MAX6675_SO, MAX6675_SCK, MSBFIRST);
  v <<= 8;
  v |= shiftIn(MAX6675_SO, MAX6675_SCK, MSBFIRST);
  digitalWrite(MAX6675_CS, HIGH);

  if (v & 0x4) {
    return NAN; // bit 2 set => no thermocouple attached
  }
  v >>= 3; // drop status bits; remaining count is in 0.25 °C steps
  return v * 0.25 - TEMP_OFFSET;
}

// recordReading folds one raw reading into the moving-average window and updates
// avgTemp. NaN readings (open thermocouple) are ignored so a transient glitch
// can't poison the average — avgTemp simply holds its previous value.
void recordReading(double t) {
  if (isnan(t)) {
    return;
  }
  sampleBuf[sampleHead] = t;
  sampleHead = (sampleHead + 1) % AVG_SAMPLES;
  if (sampleCount < AVG_SAMPLES) {
    sampleCount++;
  }
  double sum = 0.0;
  for (int i = 0; i < sampleCount; i++) {
    sum += sampleBuf[i];
  }
  avgTemp = sum / sampleCount;
}

// resetAverage clears the moving-average window (used when the room changes, so
// the new room's reading isn't blended with the old room's).
void resetAverage() {
  sampleCount = 0;
  sampleHead = 0;
  avgTemp = NAN;
}

// renderDisplay shows the temperature + selected room on the OLED.
void renderDisplay(double t) {
  if (!haveDisplay) {
    return;
  }
  display.clearDisplay();
  display.setTextColor(SSD1306_WHITE);

  // Main readout: T: <temperature> C
  display.setTextSize(2);
  display.setCursor(0, 2);
  if (isnan(t)) {
    display.print("T: -- C");
  } else {
    display.print("T: ");
    display.print(t, 1);
    display.print("C");
  }

  // Room line.
  display.setTextSize(1);
  display.setCursor(0, 30);
  if (roomIndex < 0) {
    display.print("press button: room");
  } else {
    display.print("room: ");
    display.print(ROOMS[roomIndex]);
  }

  // Connection / state line.
  display.setCursor(0, 50);
  if (paused) {
    display.print("PAUSED");
  } else if (WiFi.status() != WL_CONNECTED) {
    display.print("wifi: down");
  } else if (!mqtt.connected()) {
    display.print("mqtt: down");
  } else if (roomIndex < 0) {
    display.print("idle (no room)");
  } else {
    display.print("online");
  }
  display.display();
}

void ensureWifi() {
  if (WiFi.status() == WL_CONNECTED) {
    return;
  }
  Serial.print("WiFi connecting");
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  unsigned long start = millis();
  while (WiFi.status() != WL_CONNECTED) {
    if (millis() - start > 15000) {
      Serial.println(" timeout; will retry");
      return; // bail; loop() calls us again
    }
    delay(250);
    Serial.print(".");
  }
  Serial.print(" connected: ");
  Serial.println(WiFi.localIP());
}

void ensureMqtt() {
  if (mqtt.connected()) {
    return;
  }
  if (mqtt.connect("temp-sensor")) {
    Serial.println("MQTT connected");
  } else {
    Serial.print("MQTT connect failed, rc=");
    Serial.println(mqtt.state());
    delay(2000); // brief backoff; loop() retries
  }
}

// selectNextRoom advances the room (first press selects soverom) and points the
// publish topic at it, starting a fresh moving average.
void selectNextRoom() {
  roomIndex = (roomIndex + 1) % NROOMS;
  snprintf(topic, sizeof(topic), "sensors/%s/temperature", ROOMS[roomIndex]);
  resetAverage();
  // Restart the publish timer from the switch, so the first send under the new
  // room is at least one interval (~5s) later — giving the thermocouple time to
  // settle in the new room and the moving average time to fill.
  lastPublish = millis();
  Serial.print("room selected: ");
  Serial.println(ROOMS[roomIndex]);
}

void setup() {
  Serial.begin(115200);

  pinMode(BTN_ROOM, INPUT_PULLDOWN);
  pinMode(BTN_PAUSE, INPUT_PULLDOWN);
  attachInterrupt(digitalPinToInterrupt(BTN_ROOM), onRoomButton, FALLING);
  attachInterrupt(digitalPinToInterrupt(BTN_PAUSE), onPauseButton, FALLING);

  Wire.begin();
  haveDisplay = display.begin(SSD1306_SWITCHCAPVCC, OLED_ADDR);
  if (haveDisplay) {
    display.clearDisplay();
    display.setTextColor(SSD1306_WHITE);
    display.setTextSize(1);
    display.setCursor(0, 0);
    display.println("temp sensor");
    display.println("press button to");
    display.println("select a room");
    display.display();
  } else {
    Serial.println("SSD1306 not found; continuing without display");
  }

  WiFi.mode(WIFI_STA);
  mqtt.setServer(MQTT_BROKER, MQTT_PORT);
  ensureWifi();
}

void loop() {
  ensureWifi();
  ensureMqtt();
  mqtt.loop();

  // Buttons.
  if (roomEvent) {
    roomEvent = false;
    selectNextRoom();
    renderDisplay(avgTemp);
  }
  if (pauseEvent) {
    pauseEvent = false;
    paused = !paused;
    Serial.println(paused ? "publishing paused" : "publishing resumed");
    renderDisplay(avgTemp);
  }

  // Sample the thermocouple into the moving average and refresh the display ~1 Hz.
  if (millis() - lastRead >= READ_INTERVAL_MS) {
    lastRead = millis();
    double raw = readThermocouple();
    if (firstReadingSkipped) {
      recordReading(raw);
    } else {
      firstReadingSkipped = true; // discard the very first (unreliable) sample
    }
    renderDisplay(avgTemp);
  }

  // Publish the moving average every PUBLISH_INTERVAL_MS — but only once a room
  // has been selected (roomIndex >= 0).
  // Publish the moving average every PUBLISH_INTERVAL_MS — only once a room is
  // selected and not paused. While the average is still empty (just switched /
  // open probe) the timer isn't consumed, so it publishes as soon as a reading
  // is valid.
  if (roomIndex >= 0 && !paused && millis() - lastPublish >= PUBLISH_INTERVAL_MS) {
    if (!isnan(avgTemp)) {
      lastPublish = millis();
      char payload[96];
      snprintf(payload, sizeof(payload),
               "{\"value\":%.2f,\"unit\":\"C\",\"sensor_id\":\"%s\"}", avgTemp, ROOMS[roomIndex]);
      bool ok = mqtt.connected() && mqtt.publish(topic, payload);
      Serial.print("publish ");
      Serial.print(topic);
      Serial.print(" -> ");
      Serial.print(payload);
      Serial.println(ok ? " [ok]" : " [FAILED]");
    }
  }
}
