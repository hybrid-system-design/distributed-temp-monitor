package ingest

import (
	"math"
	"testing"
	"time"
)

// FuzzParseSample throws arbitrary topics and payloads at the wire-format trust
// boundary. The contract: parseSample must never panic, and whenever it accepts
// a message (ok), the result must satisfy the storage invariants (non-empty
// sensor_id and unit, finite value). Run: go test -run x -fuzz FuzzParseSample
func FuzzParseSample(f *testing.F) {
	now := time.Now()

	seeds := []string{
		`{"value":19.4,"unit":"C","timestamp":"2026-06-03T12:00:00Z","sensor_id":"fermenter-1"}`,
		`{"value":0}`,
		`{"value":-12.5,"sensor_id":"x"}`,
		`{"value":1e9,"timestamp":"garbage"}`,
		`{"value":"not-a-number"}`,
		`{}`,
		``,
		`not json at all`,
		`[1,2,3]`,
	}
	for _, s := range seeds {
		f.Add("sensors/fermenter-1/temperature", []byte(s))
	}

	f.Fuzz(func(t *testing.T, topic string, raw []byte) {
		s, _, ok := parseSample(topic, raw, now, 50*time.Hour, 5*time.Minute)
		if !ok {
			return // dropped messages carry no guarantees
		}
		if s.sensorID == "" {
			t.Errorf("accepted but empty sensor_id (topic=%q raw=%q)", topic, raw)
		}
		if s.unit == "" {
			t.Errorf("accepted but empty unit (raw=%q)", raw)
		}
		if math.IsNaN(s.value) || math.IsInf(s.value, 0) {
			t.Errorf("accepted but non-finite value %v (raw=%q)", s.value, raw)
		}
	})
}
