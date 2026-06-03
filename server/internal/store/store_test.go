package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndLatest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Unknown sensor -> not found, no error.
	if _, ok, err := s.Latest(ctx, "ghost"); err != nil || ok {
		t.Fatalf("Latest(ghost) = ok %v err %v; want ok=false err=nil", ok, err)
	}

	// Latest is the row with the greatest received_at, even if its event_time
	// is older (the replay/backfill case).
	mustInsert(t, s, "s1", 10.0, 100, 1000) // received_at 1000
	mustInsert(t, s, "s1", 11.0, 999, 2000) // received_at 2000 (latest), older event_time
	mustInsert(t, s, "s1", 12.0, 50, 1500)  // received_at 1500

	l, ok, err := s.Latest(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("Latest(s1) = ok %v err %v", ok, err)
	}
	if l.Value != 11.0 || l.ReceivedAt != 2000 || l.EventTime != 999 {
		t.Fatalf("Latest(s1) = %+v; want value=11 received_at=2000 event_time=999", l)
	}
}

func TestHistoryBucketing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// bucket size 100s. Two samples in bucket [0,100), one in [100,200).
	mustInsert(t, s, "s1", 10.0, 10, 0)
	mustInsert(t, s, "s1", 20.0, 90, 0)  // same bucket as above
	mustInsert(t, s, "s1", 30.0, 150, 0) // next bucket
	// A sample for a different sensor must not leak in.
	mustInsert(t, s, "other", 99.0, 50, 0)

	buckets, err := s.History(ctx, "s1", 0, 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(buckets), buckets)
	}

	b0 := buckets[0]
	if b0.T != 0 || b0.N != 2 || b0.Avg != 15.0 || b0.Min != 10.0 || b0.Max != 20.0 {
		t.Errorf("bucket0 = %+v; want T=0 N=2 Avg=15 Min=10 Max=20", b0)
	}
	b1 := buckets[1]
	if b1.T != 100 || b1.N != 1 || b1.Avg != 30.0 {
		t.Errorf("bucket1 = %+v; want T=100 N=1 Avg=30", b1)
	}
}

func TestHistoryFromCutoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mustInsert(t, s, "s1", 10.0, 100, 0) // before cutoff
	mustInsert(t, s, "s1", 20.0, 500, 0) // after cutoff

	buckets, err := s.History(ctx, "s1", 300, 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Avg != 20.0 {
		t.Fatalf("got %+v; want single bucket avg=20 (cutoff drops event_time<300)", buckets)
	}
}

func mustInsert(t *testing.T, s *Store, sensorID string, value float64, eventTime, receivedAt int64) {
	t.Helper()
	if err := s.Insert(context.Background(), sensorID, value, "C", eventTime, receivedAt); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}
