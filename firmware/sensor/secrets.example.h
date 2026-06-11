// Copy this file to `secrets.h` and fill in your values.
// `secrets.h` is gitignored and must never be committed.
#pragma once

#define WIFI_SSID     "your-wifi-ssid"
#define WIFI_PASSWORD "your-wifi-password"

// Host running the MQTT broker (the Go service's docker-compose stack).
#define MQTT_BROKER   "192.168.1.50"
#define MQTT_PORT     1883

// Note: the room/sensor name is chosen on the device with the button
// (soverom / jenterom / gutterom / stue), not configured here.
