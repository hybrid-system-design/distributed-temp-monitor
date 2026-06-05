// Command tempmon is the temperature-monitor server: it subscribes to MQTT,
// persists samples to SQLite, and serves the HTTP API consumed by the display.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tempmon/internal/api"
	"tempmon/internal/config"
	"tempmon/internal/ingest"
	"tempmon/internal/store"
)

func main() {
	logger := log.New(os.Stdout, "tempmon ", log.LstdFlags|log.Lmsgprefix)
	cfg := config.Load()

	logger.Printf("starting: http=%s mqtt=%s topic=%q db=%s", cfg.HTTPAddr, cfg.MQTTURL, cfg.MQTTTopic, cfg.DBPath)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Fatalf("store: %v", err)
	}
	defer st.Close()

	// HTTP server.
	srv := api.New(st, cfg, logger)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Printf("http: listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("http: %v", err)
		}
	}()

	// MQTT ingest.
	ing := ingest.New(cfg, st, logger)
	if err := ing.Start(); err != nil {
		logger.Fatalf("mqtt: %v", err)
	}

	// Block until interrupted, then shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logger.Printf("shutdown: signal received")

	ing.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("http: shutdown error: %v", err)
	}
	logger.Printf("shutdown: complete")
}
