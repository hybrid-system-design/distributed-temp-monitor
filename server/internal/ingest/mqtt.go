// Package ingest subscribes to the MQTT broker and writes each valid sample to
// the store. It is deliberately resilient: bad payloads are logged and dropped,
// never fatal, and the client auto-reconnects (re-subscribing on every connect).
package ingest

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"tempmon/internal/config"
	"tempmon/internal/store"
)

// payload is the MQTT JSON contract. Value is a pointer so we can distinguish
// "absent" from "0.0".
type payload struct {
	Value     *float64 `json:"value"`
	Unit      string   `json:"unit"`
	Timestamp string   `json:"timestamp"`
	SensorID  string   `json:"sensor_id"`
}

// Ingestor owns the MQTT client and feeds samples into the store.
type Ingestor struct {
	cfg    config.Config
	store  *store.Store
	log    *log.Logger
	client mqtt.Client
}

// New constructs an Ingestor.
func New(cfg config.Config, st *store.Store, logger *log.Logger) *Ingestor {
	return &Ingestor{cfg: cfg, store: st, log: logger}
}

// Start connects to the broker and subscribes. With connect-retry enabled it
// returns without error even if the broker is not yet reachable; the client
// keeps retrying and (re)subscribes via the OnConnect handler.
func (in *Ingestor) Start() error {
	opts := mqtt.NewClientOptions().
		AddBroker(in.cfg.MQTTURL).
		SetClientID(in.cfg.MQTTClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(in.cfg.MQTTConnectRetryInterval).
		SetMaxReconnectInterval(in.cfg.MQTTMaxReconnectInterval).
		SetOnConnectHandler(func(c mqtt.Client) {
			in.log.Printf("mqtt: connected, subscribing to %q", in.cfg.MQTTTopic)
			// CleanSession drops subscriptions on reconnect, so re-subscribe
			// every time we (re)connect.
			t := c.Subscribe(in.cfg.MQTTTopic, 1, in.handle)
			if t.Wait() && t.Error() != nil {
				in.log.Printf("mqtt: subscribe error: %v", t.Error())
			}
		}).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			in.log.Printf("mqtt: connection lost: %v", err)
		})

	in.client = mqtt.NewClient(opts)
	tok := in.client.Connect()
	// Don't block startup if the broker is slow/down: connect-retry handles it.
	if tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		in.log.Printf("mqtt: initial connect error (will keep retrying): %v", tok.Error())
	}
	return nil
}

// Stop disconnects the client cleanly.
func (in *Ingestor) Stop() {
	if in.client != nil && in.client.IsConnectionOpen() {
		in.client.Disconnect(250)
	} else if in.client != nil {
		in.client.Disconnect(0)
	}
}

// handle validates and stores one incoming message. It never panics or returns
// errors upward; ingestion must survive any malformed input.
func (in *Ingestor) handle(_ mqtt.Client, msg mqtt.Message) {
	now := time.Now()
	s, reason, ok := parseSample(msg.Topic(), msg.Payload(), now, in.cfg.SanityPast, in.cfg.SanityFuture)
	if !ok {
		in.log.Printf("ingest: drop message on %q: %s", msg.Topic(), reason)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := in.store.Insert(ctx, s.sensorID, s.value, s.unit, s.eventTime, now.Unix()); err != nil {
		in.log.Printf("ingest: store insert failed for %s: %v", s.sensorID, err)
	}
}

// parsedSample is the validated, storable result of an incoming message.
type parsedSample struct {
	sensorID  string
	value     float64
	unit      string
	eventTime int64
}

// parseSample validates and normalizes a raw MQTT message into a storable
// sample. ok is false (with a human reason) when the payload is unusable, in
// which case callers log-and-drop. It is pure (no I/O), so it can be unit- and
// fuzz-tested directly — this is the wire-format trust boundary.
func parseSample(topic string, raw []byte, now time.Time, past, future time.Duration) (parsedSample, string, bool) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return parsedSample{}, "bad json", false
	}

	sensorID := p.SensorID
	if sensorID == "" {
		sensorID = sensorFromTopic(topic)
	}
	if p.Value == nil || sensorID == "" {
		return parsedSample{}, "missing value or sensor_id", false
	}
	if math.IsNaN(*p.Value) || math.IsInf(*p.Value, 0) {
		return parsedSample{}, "non-finite value", false
	}

	unit := p.Unit
	if unit == "" {
		unit = "C"
	}

	var (
		ts    time.Time
		hasTS bool
	)
	if p.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
			ts, hasTS = parsed, true
		}
	}
	eventTime := resolveEventTime(ts, hasTS, now, past, future)

	return parsedSample{sensorID: sensorID, value: *p.Value, unit: unit, eventTime: eventTime}, "", true
}

// resolveEventTime returns the canonical series time. It honors a sensor-reported
// timestamp only when it falls within [now-past, now+future]; otherwise (absent,
// unparseable, or wildly off due to a bad sensor clock) it falls back to the
// server arrival time. This lets a replay of the last 48h backfill correctly
// while rejecting 1970/far-future timestamps from an unsynced sensor.
func resolveEventTime(ts time.Time, hasTS bool, now time.Time, past, future time.Duration) int64 {
	if hasTS {
		lower := now.Add(-past)
		upper := now.Add(future)
		if !ts.Before(lower) && !ts.After(upper) {
			return ts.Unix()
		}
	}
	return now.Unix()
}

// sensorFromTopic extracts the <sensor_id> segment from "sensors/<id>/temperature".
func sensorFromTopic(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
