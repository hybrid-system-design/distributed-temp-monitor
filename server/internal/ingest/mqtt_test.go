package ingest

import (
	"testing"
	"time"
)

func TestResolveEventTime(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	past := 50 * time.Hour
	future := 5 * time.Minute

	cases := []struct {
		name  string
		ts    time.Time
		hasTS bool
		want  int64
	}{
		{"no timestamp -> arrival", time.Time{}, false, now.Unix()},
		{"recent within window -> honored", now.Add(-2 * time.Hour), true, now.Add(-2 * time.Hour).Unix()},
		{"48h ago within window -> honored", now.Add(-48 * time.Hour), true, now.Add(-48 * time.Hour).Unix()},
		{"too old (epoch 1970) -> arrival", time.Unix(0, 0), true, now.Unix()},
		{"far future -> arrival", now.Add(24 * time.Hour), true, now.Unix()},
		{"slightly future within skew -> honored", now.Add(2 * time.Minute), true, now.Add(2 * time.Minute).Unix()},
		{"just past lower edge -> arrival", now.Add(-past - time.Second), true, now.Unix()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveEventTime(c.ts, c.hasTS, now, past, future)
			if got != c.want {
				t.Errorf("resolveEventTime = %d, want %d", got, c.want)
			}
		})
	}
}

func TestSensorFromTopic(t *testing.T) {
	cases := map[string]string{
		"sensors/fermenter-1/temperature": "fermenter-1",
		"sensors/abc/temperature":         "abc",
		"nope":                            "",
		"":                                "",
	}
	for topic, want := range cases {
		if got := sensorFromTopic(topic); got != want {
			t.Errorf("sensorFromTopic(%q) = %q, want %q", topic, got, want)
		}
	}
}
