package recorder

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/onvif"
)

// recordProcess manages the recording lifecycle for an individual camera.
type recordProcess struct {
	camera config.Camera
	cancel context.CancelFunc // cancels camera loop
	active bool
	mu     sync.RWMutex
}

// Engine manages FFmpeg continuous recording processes for all cameras.
type Engine struct {
	cfg       *config.Config
	processes map[string]*recordProcess
	cleaner   *Cleaner
	parentCtx context.Context
	mu        sync.RWMutex
}

// New creates a new NVR recording Engine.
func New(cfg *config.Config) *Engine {
	return &Engine{
		cfg:       cfg,
		processes: make(map[string]*recordProcess),
		cleaner:   NewCleaner(cfg),
	}
}

// StartAll initializes and launches continuous recording for all eligible cameras.
func (e *Engine) StartAll(ctx context.Context) error {
	e.mu.Lock()
	e.parentCtx = ctx
	e.mu.Unlock()

	if !e.cfg.Recordings.Enabled {
		log.Printf("[RECORDER] Built-in NVR is disabled in config")
		return nil
	}

	recordingsDir := e.cfg.Recordings.Dir
	if recordingsDir == "" {
		recordingsDir = "./rekaman_cctv"
	}
	if err := os.MkdirAll(recordingsDir, 0755); err != nil {
		return fmt.Errorf("create recordings dir %s: %w", recordingsDir, err)
	}

	log.Printf("[RECORDER] Starting built-in NVR (segment: %dm, retention: %dd, dir: %s)...",
		e.cfg.Recordings.SegmentMinutes, e.cfg.Recordings.RetentionDays, recordingsDir)

	for _, cam := range e.cfg.Cameras {
		if cam.IsRecordingEnabled() {
			e.startCamera(ctx, cam)
		}
	}

	// Start auto-retention cleaner
	e.cleaner.Start(ctx)

	return nil
}

// StopAll gracefully terminates all recording processes.
func (e *Engine) StopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id, proc := range e.processes {
		proc.mu.Lock()
		if proc.cancel != nil {
			proc.cancel()
		}
		proc.active = false
		proc.mu.Unlock()
		log.Printf("[RECORDER][%s] Recording stopped", id)
	}
	e.processes = make(map[string]*recordProcess)
}

// AddCamera dynamically registers and starts recording for a new camera.
func (e *Engine) AddCamera(ctx context.Context, cam config.Camera) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.cfg.Recordings.Enabled || !cam.IsRecordingEnabled() {
		return
	}

	e.startCamera(ctx, cam)
}

// UpdateCamera updates and restarts recording for an existing camera.
func (e *Engine) UpdateCamera(ctx context.Context, cam config.Camera) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if proc, exists := e.processes[cam.ID]; exists {
		proc.mu.Lock()
		if proc.cancel != nil {
			proc.cancel()
		}
		proc.active = false
		proc.mu.Unlock()
		delete(e.processes, cam.ID)
	}

	if e.cfg.Recordings.Enabled && cam.IsRecordingEnabled() {
		e.startCamera(ctx, cam)
	}
}

// RemoveCamera stops and removes a camera from recording.
func (e *Engine) RemoveCamera(camID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if proc, exists := e.processes[camID]; exists {
		proc.mu.Lock()
		if proc.cancel != nil {
			proc.cancel()
		}
		proc.active = false
		proc.mu.Unlock()
		delete(e.processes, camID)
		log.Printf("[RECORDER][%s] Camera removed from recorder", camID)
	}
}

// RestartAll restarts all recording processes with updated configuration (e.g. segment minutes).
func (e *Engine) RestartAll(ctx context.Context) {
	e.StopAll()
	if e.cfg.Recordings.Enabled {
		for _, cam := range e.cfg.Cameras {
			if cam.IsRecordingEnabled() {
				e.mu.Lock()
				e.startCamera(ctx, cam)
				e.mu.Unlock()
			}
		}
	}
}

// startCamera initiates a recording goroutine for a camera (must be called with e.mu held or before concurrency).
func (e *Engine) startCamera(ctx context.Context, cam config.Camera) {
	// Destination folder per camera: /recordings/{cam_id}
	camDir := filepath.Join(e.cfg.Recordings.Dir, cam.ID)
	if err := os.MkdirAll(camDir, 0755); err != nil {
		log.Printf("[RECORDER][%s] Gagal buat folder rekaman: %v", cam.ID, err)
		return
	}

	camCtx, cancel := context.WithCancel(ctx)
	proc := &recordProcess{
		camera: cam,
		cancel: cancel,
		active: true,
	}
	e.processes[cam.ID] = proc

	go e.runRecordLoop(camCtx, proc, camDir)
}

// runRecordLoop continuously records the camera stream into segmented MP4 files.
func (e *Engine) runRecordLoop(ctx context.Context, proc *recordProcess, camDir string) {
	camID := proc.camera.ID

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Step 1: Resolve RTSP URL
		rtspURL := proc.camera.URL
		if rtspURL == "" && proc.camera.OnvifURL != "" {
			discovered, err := onvif.GetStreamURI(proc.camera.OnvifURL, proc.camera.Username, proc.camera.Password)
			if err != nil {
				log.Printf("[RECORDER][%s] ONVIF discovery failed: %v", camID, err)
				sleepOrCancel(ctx, 10*time.Second)
				continue
			}
			rtspURL = discovered
		}

		if rtspURL == "" {
			log.Printf("[RECORDER][%s] No RTSP URL available for recording", camID)
			sleepOrCancel(ctx, 30*time.Second)
			continue
		}

		// Step 2: Compute segment duration in seconds (default: 300s = 5m)
		segmentSec := e.cfg.Recordings.SegmentMinutes * 60
		if segmentSec <= 0 {
			segmentSec = 300
		}

		// Output pattern: /recordings/{cam_id}/%Y-%m-%dT%H-%M-%S.mp4
		outPattern := filepath.Join(camDir, "%Y-%m-%dT%H-%M-%S.mp4")

		log.Printf("[RECORDER][%s] Recording stream into %s (segment: %ds)...", camID, outPattern, segmentSec)

		err := e.runFFmpegRecord(ctx, rtspURL, outPattern, segmentSec)
		if ctx.Err() != nil {
			return
		}

		log.Printf("[RECORDER][%s] Recording process ended: %v — restarting in 5s...", camID, err)
		sleepOrCancel(ctx, 5*time.Second)
	}
}

// runFFmpegRecord executes FFmpeg with segment copy-codec flags.
func (e *Engine) runFFmpegRecord(ctx context.Context, rtspURL, outPattern string, segmentSec int) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		// Network connection settings
		"-rtsp_transport", "tcp",
		"-timeout", "5000000", // 5 seconds timeout
		"-i", rtspURL,
		// Stream copy (zero CPU re-encoding)
		"-c:v", "copy",
		"-c:a", "copy",
		// Segment muxer
		"-f", "segment",
		"-segment_time", strconv.Itoa(segmentSec),
		"-segment_atclocktime", "1",
		"-reset_timestamps", "1",
		"-strftime", "1",
		"-segment_format", "mp4",
		// Faststart for instant web streaming
		"-movflags", "+faststart",
		outPattern,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr

	// Set TZ=UTC so %Y-%m-%dT%H-%M-%S.mp4 produces ISO UTC timestamps,
	// maintaining 100% compatibility with Shinobi legacy files and timeline parsers.
	cmd.Env = append(os.Environ(), "TZ=UTC")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start recording ffmpeg: %w", err)
	}

	return cmd.Wait()
}

func sleepOrCancel(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
