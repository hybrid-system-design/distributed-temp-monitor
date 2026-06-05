//go:build integration

// Package integration exercises the full ingest path against a real Mosquitto
// broker (started via testcontainers): MQTT publish -> subscribe -> SQLite ->
// HTTP. It proves the electronics/IT seam and its failure modes, which unit
// tests cannot. Run with: go test -tags integration ./internal/integration/...
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"tempmon/internal/api"
	"tempmon/internal/config"
	"tempmon/internal/ingest"
	"tempmon/internal/store"
)

// --- response shapes (mirror the api package's JSON contract) ---

type currentResp struct {
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

type historyResp struct {
	SensorID      string  `json:"sensor_id"`
	BucketSeconds int     `json:"bucket_seconds"`
	Points        []point `json:"points"`
}

// TestPipeline covers the success-path seam plus bad-payload resilience against
// a single shared broker. Each subtest uses its own sensor_id for isolation.
func TestPipeline(t *testing.T) {
	hostPort := startBroker(t)
	brokerURL := "tcp://" + hostPort
	baseURL := startService(t, brokerURL, 2*time.Second)
	pub := newPublisher(t, brokerURL)

	t.Run("round_trip", func(t *testing.T) {
		pub.send(t, "rt-1", 21.5, time.Now())
		got := waitForCurrent(t, baseURL, "rt-1", 21.5, 10*time.Second)
		if got.Unit != "C" || got.Stale {
			t.Fatalf("unexpected current: %+v", got)
		}
	})

	t.Run("history_downsampling", func(t *testing.T) {
		// 13 samples one minute apart over the last 12 minutes; with the default
		// 600s bucket they must collapse into far fewer points while preserving
		// the total sample count.
		const n = 13
		now := time.Now()
		for i := 0; i < n; i++ {
			pub.send(t, "hist-1", 20.0+float64(i)*0.1, now.Add(time.Duration(-(n-1-i))*time.Minute))
		}
		var h historyResp
		waitFor(t, 10*time.Second, func() bool {
			getJSON(t, baseURL+"/api/history?sensor_id=hist-1&hours=1", &h)
			return sumN(h.Points) >= n
		})
		if sumN(h.Points) != n {
			t.Fatalf("sum(n) = %d, want %d", sumN(h.Points), n)
		}
		if len(h.Points) >= n {
			t.Fatalf("got %d points; expected downsampling to reduce below %d", len(h.Points), n)
		}
		if !ascending(h.Points) {
			t.Fatalf("points not ascending by time: %+v", h.Points)
		}
	})

	t.Run("bad_payload_resilience", func(t *testing.T) {
		// Garbage on the topic must not kill ingestion: a valid sample published
		// right after still lands.
		pub.raw(t, "sensors/bad-1/temperature", []byte("}{ not json"))
		pub.raw(t, "sensors/bad-1/temperature", []byte(`{"unit":"C"}`)) // missing value
		pub.send(t, "bad-1", 7.2, time.Now())
		got := waitForCurrent(t, baseURL, "bad-1", 7.2, 10*time.Second)
		if got.Value != 7.2 {
			t.Fatalf("survivor sample missing: %+v", got)
		}
	})

	t.Run("staleness", func(t *testing.T) {
		pub.send(t, "stale-1", 4.0, time.Now())
		got := waitForCurrent(t, baseURL, "stale-1", 4.0, 10*time.Second)
		if got.Stale {
			t.Fatalf("fresh sample reported stale: %+v", got)
		}
		// Threshold is 2s; wait it out and re-check (no new publishes).
		time.Sleep(3 * time.Second)
		var c currentResp
		getJSON(t, baseURL+"/api/current?sensor_id=stale-1", &c)
		if !c.Stale {
			t.Fatalf("sample not reported stale after threshold: %+v", c)
		}
	})
}

// TestReconnectAfterBrokerBounce proves the subscriber re-establishes after its
// connection to the broker drops and returns — the network-resilience story for
// field deployments. The service connects through an in-process TCP proxy; we
// drop and restore the proxy (broker stays up) to simulate a network blip.
func TestReconnectAfterBrokerBounce(t *testing.T) {
	hostPort := startBroker(t)
	proxy := newProxy(t, hostPort)

	// Service talks to the broker through the proxy; the publisher talks direct.
	baseURL := startService(t, "tcp://"+proxy.addr(), 2*time.Minute)
	pub := newPublisher(t, "tcp://"+hostPort)

	pub.send(t, "rc-1", 1.0, time.Now())
	waitForCurrent(t, baseURL, "rc-1", 1.0, 10*time.Second)

	// Sever the service's link, then restore it.
	proxy.stop()
	proxy.start()

	// Once the service auto-reconnects and re-subscribes, a fresh value flows.
	// QoS1/clean-session means messages sent while disconnected are not retained,
	// so we keep publishing until one lands.
	waitFor(t, 30*time.Second, func() bool {
		pub.send(t, "rc-1", 2.0, time.Now())
		var c currentResp
		if !tryGetJSON(baseURL+"/api/current?sensor_id=rc-1", &c) {
			return false
		}
		return c.Value == 2.0
	})
}

// --- broker / service harness ---

// startBroker launches eclipse-mosquitto and returns its host:port.
func startBroker(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:2",
		ExposedPorts: []string{"1883/tcp"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      writeMosquittoConf(t),
			ContainerFilePath: "/mosquitto/config/mosquitto.conf",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("broker host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "1883/tcp")
	if err != nil {
		t.Fatalf("broker port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}

// startService boots the store + HTTP API + MQTT ingestor in-process and returns
// the API base URL.
func startService(t *testing.T, brokerURL string, stale time.Duration) string {
	t.Helper()
	cfg := config.Config{
		MQTTURL:                  brokerURL,
		MQTTTopic:                "sensors/+/temperature",
		MQTTClientID:             "tempmon-itest-" + randSuffix(),
		StaleThreshold:           stale,
		SanityPast:               50 * time.Hour,
		SanityFuture:             5 * time.Minute,
		MQTTConnectRetryInterval: 500 * time.Millisecond,
		MQTTMaxReconnectInterval: 1 * time.Second,
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	logger := log.New(io.Discard, "", 0)
	ts := httptest.NewServer(api.New(st, cfg, logger).Handler())
	t.Cleanup(ts.Close)

	ing := ingest.New(cfg, st, logger)
	if err := ing.Start(); err != nil {
		t.Fatalf("ingest.Start: %v", err)
	}
	t.Cleanup(ing.Stop)
	return ts.URL
}

// --- publisher ---

type publisher struct{ client mqtt.Client }

func newPublisher(t *testing.T, brokerURL string) *publisher {
	t.Helper()
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("itest-pub-" + randSuffix()).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(300 * time.Millisecond)
	c := mqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		t.Fatalf("publisher connect: %v", tok.Error())
	}
	t.Cleanup(func() { c.Disconnect(100) })
	return &publisher{client: c}
}

func (p *publisher) send(t *testing.T, sensorID string, value float64, ts time.Time) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"value": value, "unit": "C",
		"timestamp": ts.UTC().Format(time.RFC3339), "sensor_id": sensorID,
	})
	p.raw(t, "sensors/"+sensorID+"/temperature", body)
}

func (p *publisher) raw(t *testing.T, topic string, body []byte) {
	t.Helper()
	tok := p.client.Publish(topic, 1, false, body)
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		// During a blip the publisher may be mid-reconnect; let the caller's
		// retry loop handle it rather than failing hard.
		t.Logf("publish to %q pending/failed: %v", topic, tok.Error())
	}
}

// --- in-process TCP proxy (controllable broker link) ---

type tcpProxy struct {
	t      *testing.T
	target string
	port   string

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
}

func newProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	p := &tcpProxy{t: t, target: target, conns: map[net.Conn]struct{}{}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	p.port = port
	p.ln = l
	go p.acceptLoop(l)
	t.Cleanup(p.stop)
	return p
}

func (p *tcpProxy) addr() string { return "127.0.0.1:" + p.port }

func (p *tcpProxy) acceptLoop(l net.Listener) {
	for {
		client, err := l.Accept()
		if err != nil {
			return // listener closed
		}
		go p.pipe(client)
	}
}

func (p *tcpProxy) pipe(client net.Conn) {
	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		client.Close()
		return
	}
	p.track(client)
	p.track(upstream)
	go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
	_, _ = io.Copy(client, upstream)
	client.Close()
}

func (p *tcpProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

// stop closes the listener and all live connections, severing the service link.
func (p *tcpProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		c.Close()
		delete(p.conns, c)
	}
}

// start re-listens on the same port.
func (p *tcpProxy) start() {
	l, err := net.Listen("tcp", p.addr())
	if err != nil {
		p.t.Fatalf("proxy relisten: %v", err)
	}
	p.mu.Lock()
	p.ln = l
	p.mu.Unlock()
	go p.acceptLoop(l)
}

// --- polling / assertion helpers ---

func waitForCurrent(t *testing.T, baseURL, sensorID string, want float64, timeout time.Duration) currentResp {
	t.Helper()
	var c currentResp
	waitFor(t, timeout, func() bool {
		if !tryGetJSON(baseURL+"/api/current?sensor_id="+sensorID, &c) {
			return false
		}
		return c.Value == want
	})
	return c
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	if !tryGetJSON(url, out) {
		t.Fatalf("GET %s: no 200/JSON", url)
	}
}

func tryGetJSON(url string, out any) bool {
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(resp.Body).Decode(out) == nil
}

func sumN(points []point) int {
	total := 0
	for _, p := range points {
		total += p.N
	}
	return total
}

func ascending(points []point) bool {
	for i := 1; i < len(points); i++ {
		if points[i].T < points[i-1].T {
			return false
		}
	}
	return true
}

func writeMosquittoConf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mosquitto.conf")
	conf := "listener 1883\nallow_anonymous true\npersistence false\n"
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}
	return path
}

func randSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
