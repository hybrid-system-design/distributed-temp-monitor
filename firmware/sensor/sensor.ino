// Temperature sensor node for the distributed temperature monitor.
//
// Deliberately dumb: read a MAX6675 thermocouple (reported as a moving average,
// since it's noisy), show it on a local OLED, and publish JSON to MQTT — no
// relay, no local web server. Graceful WiFi/MQTT
// reconnection. The server stamps arrival time, so this node carries no clock
// and sends no timestamp (it is optional in the contract).
//
// Topic:   sensors/<SENSOR_ID>/temperature
// Payload: {"value":21.5,"unit":"C","sensor_id":"fermenter-1"}
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

// SSD1306 OLED (I2C) — optional local readout. The sensor runs fine without it.
#define SCREEN_WIDTH  128
#define SCREEN_HEIGHT 64
#define OLED_ADDR     0x3C

static const unsigned long PUBLISH_INTERVAL_MS = 5000;
static const unsigned long READ_INTERVAL_MS    = 1000;

// The thermocouple is noisy, so the reported value is a moving average of the
// last N readings. At one read per second that smooths over ~N seconds — the
// original unit averaged N=5.
#define AVG_SAMPLES 5

WiFiClient net;
PubSubClient mqtt(net);
Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, -1);

char topic[64];
unsigned long lastPublish = 0;
unsigned long lastRead = 0;

double sampleBuf[AVG_SAMPLES];
int sampleCount = 0;  // valid samples currently in the window (0..AVG_SAMPLES)
int sampleHead = 0;   // ring-buffer write index
double avgTemp = NAN; // current moving average (NAN until the first valid read)
bool firstReadingSkipped = false; // the MAX6675's first conversion is unreliable

bool haveDisplay = false;

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

// renderDisplay shows the temperature on the OLED (no-op if none was detected).
void renderDisplay(double t) {
  if (!haveDisplay) {
    return;
  }
  display.clearDisplay();
  display.setTextColor(SSD1306_WHITE);

  // Main readout: T: <temperature> C
  display.setTextSize(2);
  display.setCursor(0, 8);
  if (isnan(t)) {
    display.print("T: -- C");
  } else {
    display.print("T: ");
    display.print(t, 1);
    display.print(" C");
  }

  // Small connection status line.
  display.setTextSize(1);
  display.setCursor(0, 48);
  if (WiFi.status() != WL_CONNECTED) {
    display.print("wifi: down");
  } else if (!mqtt.connected()) {
    display.print("mqtt: down");
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
  String clientId = String("temp-sensor-") + SENSOR_ID;
  if (mqtt.connect(clientId.c_str())) {
    Serial.println("MQTT connected");
  } else {
    Serial.print("MQTT connect failed, rc=");
    Serial.println(mqtt.state());
    delay(2000); // brief backoff; loop() retries
  }
}

void setup() {
  Serial.begin(115200);
  snprintf(topic, sizeof(topic), "sensors/%s/temperature", SENSOR_ID);

  Wire.begin();
  haveDisplay = display.begin(SSD1306_SWITCHCAPVCC, OLED_ADDR);
  if (haveDisplay) {
    display.clearDisplay();
    display.setTextColor(SSD1306_WHITE);
    display.setTextSize(1);
    display.setCursor(0, 0);
    display.println("temp sensor");
    display.println(SENSOR_ID);
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

  // Publish the moving average every PUBLISH_INTERVAL_MS.
  if (millis() - lastPublish >= PUBLISH_INTERVAL_MS) {
    lastPublish = millis();
    if (isnan(avgTemp)) {
      Serial.println("no valid reading yet; skipping publish");
      return;
    }
    char payload[96];
    snprintf(payload, sizeof(payload),
             "{\"value\":%.2f,\"unit\":\"C\",\"sensor_id\":\"%s\"}", avgTemp, SENSOR_ID);
    bool ok = mqtt.connected() && mqtt.publish(topic, payload);
    Serial.print("publish ");
    Serial.print(topic);
    Serial.print(" -> ");
    Serial.print(payload);
    Serial.println(ok ? " [ok]" : " [FAILED]");
  }
}
