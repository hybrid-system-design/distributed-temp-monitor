// Package config loads runtime configuration from environment variables with
// sensible defaults so the service runs with zero config in dev and is fully
// tunable in the Docker stack.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime settings for the service.
type Config struct {
	// HTTPAddr is the listen address for the HTTP API, e.g. ":8080".
	HTTPAddr string
	// MQTTURL is the broker URL, e.g. "tcp://localhost:1883".
	MQTTURL string
	// MQTTTopic is the subscription filter, e.g. "sensors/+/temperature".
	MQTTTopic string
	// MQTTClientID is this subscriber's client id.
	MQTTClientID string
	// DBPath is the SQLite database file path.
	DBPath string
	// StaleThreshold: /api/current reports stale=true when a sensor's most
	// recent sample is older than this.
	StaleThreshold time.Duration
	// SanityPast / SanityFuture bound how far a sensor-reported timestamp may
	// deviate from server wall-clock before we fall back to arrival time.
	SanityPast   time.Duration
	SanityFuture time.Duration
	// MQTT reconnect tuning: interval between initial connect attempts, and the
	// cap on the auto-reconnect backoff after a dropped connection.
	MQTTConnectRetryInterval time.Duration
	MQTTMaxReconnectInterval time.Duration
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		HTTPAddr:       getenv("HTTP_ADDR", ":8080"),
		MQTTURL:        getenv("MQTT_URL", "tcp://localhost:1883"),
		MQTTTopic:      getenv("MQTT_TOPIC", "sensors/+/temperature"),
		MQTTClientID:   getenv("MQTT_CLIENT_ID", "tempmon-server"),
		DBPath:         getenv("DB_PATH", "tempmon.db"),
		StaleThreshold: getdur("STALE_THRESHOLD", 120*time.Second),
		SanityPast:     getdur("SANITY_PAST", 50*time.Hour),
		SanityFuture:   getdur("SANITY_FUTURE", 5*time.Minute),

		MQTTConnectRetryInterval: getdur("MQTT_CONNECT_RETRY_INTERVAL", 5*time.Second),
		MQTTMaxReconnectInterval: getdur("MQTT_MAX_RECONNECT_INTERVAL", 30*time.Second),
	}
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		// Accept either a Go duration string ("120s") or a bare integer
		// number of seconds ("120").
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}
