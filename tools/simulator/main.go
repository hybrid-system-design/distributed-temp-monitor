// Command simulator publishes a canned temperature series to the MQTT broker so
// demos and development never depend on the physical sensor or venue wifi.
//
// It first backfills a realistic last-N-hours history (each message carries its
// true past timestamp, which the server honors via its sanity window), then —
// with --live — keeps emitting fresh samples at the configured step.
//
//	go run . --broker tcp://localhost:1883                 # publishes as sensor "sim"
//	go run . --broker tcp://localhost:1883 --live          # backfill then keep emitting
package main

import (
	"encoding/json"
	"flag"
	"log"
	"math"
	"math/rand"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type sample struct {
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Timestamp string  `json:"timestamp"`
	SensorID  string  `json:"sensor_id"`
}

func main() {
	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URL")
	sensorID := flag.String("sensor-id", "sim", "sensor id (defaults to 'sim' so demo data never mixes with a real sensor)")
	hours := flag.Int("hours", 48, "hours of history to backfill")
	step := flag.Duration("step", 5*time.Minute, "interval between samples")
	setpoint := flag.Float64("setpoint", 19.0, "baseline temperature (C)")
	amplitude := flag.Float64("amplitude", 1.5, "diurnal swing amplitude (C)")
	live := flag.Bool("live", false, "after backfill, keep publishing fresh samples")
	flag.Parse()

	topic := "sensors/" + *sensorID + "/temperature"

	opts := mqtt.NewClientOptions().
		AddBroker(*broker).
		SetClientID("tempmon-simulator-" + *sensorID).
		SetAutoReconnect(true).
		SetConnectRetry(true)
	client := mqtt.NewClient(opts)
	if tok := client.Connect(); tok.Wait() && tok.Error() != nil {
		log.Fatalf("connect %s: %v", *broker, tok.Error())
	}
	defer client.Disconnect(250)
	log.Printf("connected to %s, publishing to %q", *broker, topic)

	// Backfill: from now-hours up to now, one message per step.
	now := time.Now()
	from := now.Add(-time.Duration(*hours) * time.Hour)
	count := 0
	for t := from; !t.After(now); t = t.Add(*step) {
		publish(client, topic, *sensorID, value(t, *setpoint, *amplitude), t)
		count++
	}
	log.Printf("backfilled %d samples over %dh", count, *hours)

	if !*live {
		return
	}
	log.Printf("live mode: emitting every %s (ctrl-c to stop)", *step)
	ticker := time.NewTicker(*step)
	defer ticker.Stop()
	for tNow := range ticker.C {
		publish(client, topic, *sensorID, value(tNow, *setpoint, *amplitude), tNow)
	}
}

// value models a slow diurnal swing around the setpoint plus small noise.
func value(t time.Time, setpoint, amplitude float64) float64 {
	frac := float64(t.Hour()*3600+t.Minute()*60+t.Second()) / 86400.0
	diurnal := amplitude * math.Sin(2*math.Pi*frac)
	noise := (rand.Float64() - 0.5) * 0.4
	return math.Round((setpoint+diurnal+noise)*10) / 10
}

func publish(client mqtt.Client, topic, sensorID string, v float64, t time.Time) {
	payload, err := json.Marshal(sample{
		Value:     v,
		Unit:      "C",
		Timestamp: t.UTC().Format(time.RFC3339),
		SensorID:  sensorID,
	})
	if err != nil {
		log.Printf("marshal: %v", err)
		return
	}
	if tok := client.Publish(topic, 1, false, payload); tok.Wait() && tok.Error() != nil {
		log.Printf("publish: %v", tok.Error())
	}
}
