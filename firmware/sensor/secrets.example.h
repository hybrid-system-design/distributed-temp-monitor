// Copy this file to `secrets.h` and fill in your values.
// `secrets.h` is gitignored and must never be committed.
#pragma once

#define WIFI_SSID     "your-wifi-ssid"
#define WIFI_PASSWORD "your-wifi-password"

// Host running the MQTT broker (the Go service's docker-compose stack).
#define MQTT_BROKER   "192.168.1.50"
#define MQTT_PORT     1883

// Identifies this sensor; publishes to sensors/<SENSOR_ID>/temperature.
#define SENSOR_ID     "fermenter-1"
