package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tempmon/internal/config"
	"tempmon/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Config{StaleThreshold: 120 * time.Second}
	return New(st, cfg, log.New(io.Discard, "", 0)), st
}

func TestCurrentNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(srv, "/api/current?sensor_id=ghost")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCurrentMissingParam(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(srv, "/api/current")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCurrentFreshVsStale(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now().Unix()

	// Fresh sample (received just now).
	if err := st.Insert(context.Background(), "fresh", 19.4, "C", now, now); err != nil {
		t.Fatal(err)
	}
	var fresh currentResponse
	decode(t, do(srv, "/api/current?sensor_id=fresh"), &fresh)
	if fresh.Stale {
		t.Errorf("fresh sample reported stale: %+v", fresh)
	}
	if fresh.Value != 19.4 {
		t.Errorf("value = %v, want 19.4", fresh.Value)
	}

	// Stale sample (received 200s ago, threshold 120s).
	old := now - 200
	if err := st.Insert(context.Background(), "old", 5.0, "C", old, old); err != nil {
		t.Fatal(err)
	}
	var stale currentResponse
	decode(t, do(srv, "/api/current?sensor_id=old"), &stale)
	if !stale.Stale {
		t.Errorf("old sample not reported stale: %+v", stale)
	}
	if stale.AgeSeconds < 190 {
		t.Errorf("age_seconds = %d, want ~200", stale.AgeSeconds)
	}
}

func TestHistoryShape(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now().Unix()
	// Two samples in the same 600s bucket within the last hour.
	if err := st.Insert(context.Background(), "s1", 10.0, "C", now-100, now-100); err != nil {
		t.Fatal(err)
	}
	if err := st.Insert(context.Background(), "s1", 20.0, "C", now-50, now-50); err != nil {
		t.Fatal(err)
	}
	var resp historyResponse
	decode(t, do(srv, "/api/history?sensor_id=s1&hours=48&bucket=600"), &resp)
	if resp.BucketSeconds != 600 || resp.SensorID != "s1" || resp.Unit != "C" {
		t.Errorf("unexpected envelope: %+v", resp)
	}
	if len(resp.Points) != 1 {
		t.Fatalf("got %d points, want 1: %+v", len(resp.Points), resp.Points)
	}
	if resp.Points[0].Avg != 15.0 || resp.Points[0].N != 2 {
		t.Errorf("point = %+v, want avg=15 n=2", resp.Points[0])
	}
}

func TestDashboardServedAtRoot(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(srv, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, marker := range []string{
		"Temperature Monitor", // title
		`id="windows"`,        // time-window button container
		`id="tip"`,            // hover tooltip element
		`id="chart"`,          // SVG chart
		`id="sensorsel"`,      // sensor/room dropdown
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("dashboard body missing expected marker %q", marker)
		}
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := do(srv, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", rec.Code)
	}
}

func TestSensorsList(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now().Unix()
	// Insert out of order, with a duplicate, across sensors.
	for _, s := range []struct {
		id string
		v  float64
	}{{"stue", 20}, {"soverom", 19}, {"stue", 21}, {"gutterom", 18}} {
		if err := st.Insert(context.Background(), s.id, s.v, "C", now, now); err != nil {
			t.Fatal(err)
		}
	}
	var resp struct {
		Sensors []string `json:"sensors"`
	}
	decode(t, do(srv, "/api/sensors"), &resp)
	want := []string{"gutterom", "soverom", "stue"} // distinct, sorted
	if len(resp.Sensors) != len(want) {
		t.Fatalf("got %v, want %v", resp.Sensors, want)
	}
	for i := range want {
		if resp.Sensors[i] != want[i] {
			t.Fatalf("got %v, want %v", resp.Sensors, want)
		}
	}
}

func TestSensorsEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	var resp struct {
		Sensors []string `json:"sensors"`
	}
	decode(t, do(srv, "/api/sensors"), &resp)
	if resp.Sensors == nil || len(resp.Sensors) != 0 {
		t.Fatalf("empty DB should give [], got %v", resp.Sensors)
	}
}

func do(srv *Server, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
}
