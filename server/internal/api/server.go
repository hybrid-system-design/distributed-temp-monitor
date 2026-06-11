// Package api serves the HTTP JSON endpoints consumed by the display node:
// /api/current (polled frequently), /api/history (the 48h downsampled series),
// and /healthz. Responses include permissive CORS so a browser/display can read
// them directly.
package api

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"tempmon/internal/config"
	"tempmon/internal/store"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	store *store.Store
	cfg   config.Config
	log   *log.Logger
}

// New constructs a Server.
func New(st *store.Store, cfg config.Config, logger *log.Logger) *Server {
	return &Server{store: st, cfg: cfg, log: logger}
}

// Handler returns the fully wired http.Handler (routes + CORS).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/api/current", s.handleCurrent)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/sensors", s.handleSensors)
	mux.HandleFunc("/healthz", s.handleHealth)
	return cors(mux)
}

type currentResponse struct {
	SensorID   string  `json:"sensor_id"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	EventTime  string  `json:"event_time"`
	ReceivedAt string  `json:"received_at"`
	AgeSeconds int64   `json:"age_seconds"`
	Stale      bool    `json:"stale"`
}

type point struct {
	T   string  `json:"t"`
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	N   int     `json:"n"`
}

type historyResponse struct {
	SensorID      string  `json:"sensor_id"`
	Unit          string  `json:"unit"`
	BucketSeconds int     `json:"bucket_seconds"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	Points        []point `json:"points"`
}

func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	sensorID := r.URL.Query().Get("sensor_id")
	if sensorID == "" {
		writeErr(w, http.StatusBadRequest, "sensor_id is required")
		return
	}
	l, ok, err := s.store.Latest(r.Context(), sensorID)
	if err != nil {
		s.log.Printf("api: current %s: %v", sensorID, err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "sensor not found")
		return
	}
	age := time.Now().Unix() - l.ReceivedAt
	if age < 0 {
		age = 0
	}
	writeJSON(w, http.StatusOK, currentResponse{
		SensorID:   sensorID,
		Value:      round1(l.Value),
		Unit:       l.Unit,
		EventTime:  unixToRFC3339(l.EventTime),
		ReceivedAt: unixToRFC3339(l.ReceivedAt),
		AgeSeconds: age,
		Stale:      age > int64(s.cfg.StaleThreshold.Seconds()),
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sensorID := q.Get("sensor_id")
	if sensorID == "" {
		writeErr(w, http.StatusBadRequest, "sensor_id is required")
		return
	}
	hours := parseIntDefault(q.Get("hours"), 48)
	if hours <= 0 {
		hours = 48
	}
	if hours > 24*30 { // clamp to 30 days
		hours = 24 * 30
	}
	bucket := parseIntDefault(q.Get("bucket"), 600)
	if bucket <= 0 {
		bucket = 600
	}

	now := time.Now()
	from := now.Add(-time.Duration(hours) * time.Hour).Unix()

	buckets, err := s.store.History(r.Context(), sensorID, from, bucket)
	if err != nil {
		s.log.Printf("api: history %s: %v", sensorID, err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	points := make([]point, 0, len(buckets))
	for _, b := range buckets {
		points = append(points, point{
			T:   unixToRFC3339(b.T),
			Avg: round1(b.Avg),
			Min: round1(b.Min),
			Max: round1(b.Max),
			N:   b.N,
		})
	}
	writeJSON(w, http.StatusOK, historyResponse{
		SensorID:      sensorID,
		Unit:          "C", // metric throughout
		BucketSeconds: bucket,
		From:          unixToRFC3339(from),
		To:            unixToRFC3339(now.Unix()),
		Points:        points,
	})
}

func (s *Server) handleSensors(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Sensors(r.Context())
	if err != nil {
		s.log.Printf("api: sensors: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if list == nil {
		list = []string{} // serialize as [] not null
	}
	writeJSON(w, http.StatusOK, map[string][]string{"sensors": list})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDashboard serves the self-contained web UI at "/". The mux routes every
// otherwise-unmatched path here, so reject anything that isn't exactly "/".
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, dashboardHTML)
}

// --- helpers ---

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func unixToRFC3339(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
