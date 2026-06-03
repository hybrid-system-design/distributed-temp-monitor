// Package ingest subscribes to the MQTT broker and writes each valid sample to
// the store. It is deliberately resilient: bad payloads are logged and dropped,
// never fatal, and the client auto-reconnects (re-subscribing on every connect).
package ingest

import (
	"context"
	"encoding/json"
	"log"
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
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
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

	var p payload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		in.log.Printf("ingest: drop bad json on %q: %v", msg.Topic(), err)
		return
	}

	sensorID := p.SensorID
	if sensorID == "" {
		sensorID = sensorFromTopic(msg.Topic())
	}
	if p.Value == nil || sensorID == "" {
		in.log.Printf("ingest: drop invalid payload on %q (value or sensor_id missing)", msg.Topic())
		return
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
		} else {
			in.log.Printf("ingest: %s bad timestamp %q, using arrival time", sensorID, p.Timestamp)
		}
	}
	eventTime := resolveEventTime(ts, hasTS, now, in.cfg.SanityPast, in.cfg.SanityFuture)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := in.store.Insert(ctx, sensorID, *p.Value, unit, eventTime, now.Unix()); err != nil {
		in.log.Printf("ingest: store insert failed for %s: %v", sensorID, err)
	}
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
