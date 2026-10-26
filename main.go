package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/recorder"
	"cctv-monitor/internal/recording"
	"cctv-monitor/internal/server"
	"cctv-monitor/internal/stream"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[FATAL] Gagal load config: %v", err)
	}

	log.Printf("[INFO] Loaded %d camera(s) from config", len(cfg.Cameras))

	// Initialize stream manager
	mgr := stream.NewManager(cfg)

	// Initialize recordings playback manager
	recMgr := recording.NewManager(cfg)
	log.Printf("[INFO] Recordings storage path: %s", recMgr.BaseDir())

	// Initialize built-in NVR recorder engine
	recEngine := recorder.New(cfg)

	// Start all camera streams and recorder
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		log.Fatalf("[FATAL] Gagal start streams: %v", err)
	}

	if err := recEngine.StartAll(ctx); err != nil {
		log.Printf("[WARN] Gagal start recorder: %v", err)
	}

	// Initialize HTTP server
	srv := server.New(cfg, mgr, recMgr, recEngine)

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("\n[INFO] Received signal %v, shutting down...\n", sig)
		cancel()
		recEngine.StopAll()
		mgr.StopAll()
		os.Exit(0)
	}()

	// Start HTTP server (blocking)
	log.Printf("[INFO] ========================================")
	log.Printf("[INFO] CCTV Monitor running on http://localhost:%d", cfg.Server.Port)
	log.Printf("[INFO] ========================================")

	if err := srv.Start(); err != nil {
		log.Fatalf("[FATAL] Server error: %v", err)
	}
}
