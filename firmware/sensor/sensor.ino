// Temperature sensor node for the distributed temperature monitor.
//
// Deliberately dumb: read a MAX6675 thermocouple, publish JSON to MQTT, repeat.
// No display, no relay, no local web server — measurement and publishing only,
// with graceful WiFi/MQTT reconnection. The server stamps arrival time, so this
// node carries no clock and sends no timestamp (it is optional in the contract).
//
// Topic:   sensors/<SENSOR_ID>/temperature
// Payload: {"value":21.5,"unit":"C","sensor_id":"fermenter-1"}
//
// Libraries: PubSubClient (knolleary). WiFi is provided by the ESP32 core.
// Copy secrets.example.h to secrets.h before building.

#include <WiFi.h>
#include <PubSubClient.h>
#include "secrets.h"

// MAX6675 thermocouple (bit-banged SPI).
#define MAX6675_SCK 18
#define MAX6675_CS  19
#define MAX6675_SO  5

// Calibration offset (°C). The reference unit read boiling water ~3 °C high.
#define TEMP_OFFSET 3.0

static const unsigned long PUBLISH_INTERVAL_MS = 5000;

WiFiClient net;
PubSubClient mqtt(net);

char topic[64];
unsigned long lastPublish = 0;

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
  WiFi.mode(WIFI_STA);
  mqtt.setServer(MQTT_BROKER, MQTT_PORT);
  ensureWifi();
}

void loop() {
  ensureWifi();
  ensureMqtt();
  mqtt.loop();

  if (millis() - lastPublish < PUBLISH_INTERVAL_MS) {
    return;
  }
  lastPublish = millis();

  double t = readThermocouple();
  if (isnan(t)) {
    Serial.println("thermocouple open; skipping publish");
    return;
  }

  char payload[96];
  snprintf(payload, sizeof(payload),
           "{\"value\":%.2f,\"unit\":\"C\",\"sensor_id\":\"%s\"}", t, SENSOR_ID);

  bool ok = mqtt.connected() && mqtt.publish(topic, payload);
  Serial.print("publish ");
  Serial.print(topic);
  Serial.print(" -> ");
  Serial.print(payload);
  Serial.println(ok ? " [ok]" : " [FAILED]");
}
