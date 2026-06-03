// Package store is the SQLite persistence layer for temperature samples.
//
// It uses the pure-Go driver modernc.org/sqlite (no cgo) so the service builds
// as a fully static binary suitable for a scratch container image.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps the SQLite database connection.
type Store struct {
	db *sql.DB
}

// Latest is the most recently received sample for a sensor.
type Latest struct {
	Value      float64
	Unit       string
	EventTime  int64 // canonical series time, unix seconds
	ReceivedAt int64 // server arrival time, unix seconds
}

// Bucket is one downsampled point of a history series.
type Bucket struct {
	T   int64   // bucket start, unix seconds
	Avg float64 // mean value in the bucket
	Min float64 // minimum value in the bucket
	Max float64 // maximum value in the bucket
	N   int     // number of raw samples in the bucket
}

const schema = `
CREATE TABLE IF NOT EXISTS samples (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  sensor_id   TEXT    NOT NULL,
  value       REAL    NOT NULL,
  unit        TEXT    NOT NULL DEFAULT 'C',
  event_time  INTEGER NOT NULL,
  received_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_samples_sensor_event ON samples(sensor_id, event_time);
`

// Open opens (creating if needed) the SQLite database at path, enables WAL mode,
// and applies the schema migration.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single writer avoids "database is locked" under concurrent ingest +
	// HTTP reads; WAL still allows concurrent readers.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;", // avoid needing a temp dir (scratch image has none)
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Insert records one sample.
func (s *Store) Insert(ctx context.Context, sensorID string, value float64, unit string, eventTime, receivedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO samples (sensor_id, value, unit, event_time, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sensorID, value, unit, eventTime, receivedAt)
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	return nil
}

// Latest returns the most recently *received* sample for a sensor. The bool is
// false (with no error) when the sensor has never been seen.
func (s *Store) Latest(ctx context.Context, sensorID string) (Latest, bool, error) {
	var l Latest
	err := s.db.QueryRowContext(ctx,
		`SELECT value, unit, event_time, received_at
		 FROM samples
		 WHERE sensor_id = ?
		 ORDER BY received_at DESC
		 LIMIT 1`, sensorID).
		Scan(&l.Value, &l.Unit, &l.EventTime, &l.ReceivedAt)
	if err == sql.ErrNoRows {
		return Latest{}, false, nil
	}
	if err != nil {
		return Latest{}, false, fmt.Errorf("latest: %w", err)
	}
	return l, true, nil
}

// History returns server-side downsampled buckets for a sensor, covering
// event_time >= from, grouped into fixed windows of bucketSeconds, ordered
// ascending by time. Bucketing and aggregation happen in SQL.
func (s *Store) History(ctx context.Context, sensorID string, from int64, bucketSeconds int) ([]Bucket, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 600
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT (event_time / ?) * ? AS bucket,
		        AVG(value), MIN(value), MAX(value), COUNT(*)
		 FROM samples
		 WHERE sensor_id = ? AND event_time >= ?
		 GROUP BY bucket
		 ORDER BY bucket ASC`,
		bucketSeconds, bucketSeconds, sensorID, from)
	if err != nil {
		return nil, fmt.Errorf("history query: %w", err)
	}
	defer rows.Close()

	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.T, &b.Avg, &b.Min, &b.Max, &b.N); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history rows: %w", err)
	}
	return out, nil
}
