package recorder

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

	// Always bind to the server's parent context so ephemeral HTTP request contexts don't terminate recording
	baseCtx := e.parentCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	camCtx, cancel := context.WithCancel(baseCtx)
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

		sessionCtx, sessionCancel := context.WithCancel(ctx)

		// Start background health watchdog to detect audio-only / video-starved streams and auto-recover
		go e.watchdogRecordingHealth(sessionCtx, camID, camDir, sessionCancel)

		err := e.runFFmpegRecord(sessionCtx, rtspURL, outPattern, segmentSec)
		sessionCancel() // Ensure watchdog stops when FFmpeg terminates

		if ctx.Err() != nil {
			return
		}

		log.Printf("[RECORDER][%s] Recording process ended: %v — restarting in 5s...", camID, err)
		sleepOrCancel(ctx, 5*time.Second)
	}
}

// watchdogRecordingHealth monitors active recording files. If a file is growing at audio-only speeds
// (< 20 KB/sec, meaning 64kbps AAC without video frames) for >= 60 seconds, it cancels the session to reconnect.
func (e *Engine) watchdogRecordingHealth(ctx context.Context, camID, camDir string, cancel context.CancelFunc) {
	// Give FFmpeg 45 seconds initial grace period to establish RTSP connection and start writing first segment
	select {
	case <-ctx.Done():
		return
	case <-time.After(45 * time.Second):
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(camDir)
			if err != nil || len(entries) == 0 {
				continue
			}

			var newestName string
			var newestMod time.Time
			var newestSize int64

			now := time.Now()
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(newestMod) {
					newestMod = info.ModTime()
					newestName = entry.Name()
					newestSize = info.Size()
				}
			}

			if newestName == "" {
				continue
			}

			// File must have been modified within the last 15 seconds to be considered active
			if now.Sub(newestMod) > 15*time.Second {
				continue
			}

			// Parse file start time from filename (%Y-%m-%dT%H-%M-%S.mp4)
			// e.g. 2026-11-04T16-00-00.mp4
			fileBase := strings.TrimSuffix(newestName, ".mp4")
			t, err := time.Parse("2006-01-02T15-04-05", fileBase)
			if err != nil {
				continue
			}

			ageSec := int(now.Sub(t).Seconds())
			// Only check files that have been recording for at least 60 seconds
			if ageSec >= 60 {
				// Audio only at 64kbps produces ~8 KB/sec (480 KB in 60s, 960 KB in 120s).
				// Video (720p/1080p H.264) produces at least 40 KB/sec to 100 KB/sec (> 2.4 MB in 60s).
				// If average rate is < 20 KB/sec, video stream is missing or stalled!
				avgKBps := float64(newestSize) / float64(ageSec*1024)
				if avgKBps < 20.0 {
					log.Printf("[RECORDER][%s] PERINGATAN: Rekaman %s hanya berisi audio (rata-rata: %.1f KB/s, ukuran: %d bytes dalam %ds). Me-restart FFmpeg untuk memulihkan video...",
						camID, newestName, avgKBps, newestSize, ageSec)
					cancel()
					return
				}
			}
		}
	}
}

// runFFmpegRecord executes FFmpeg with segment copy-codec flags.
func (e *Engine) runFFmpegRecord(ctx context.Context, rtspURL, outPattern string, segmentSec int) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		// Network connection settings
		"-rtsp_transport", "tcp",
		"-rtsp_flags", "prefer_tcp",
		"-timeout", "5000000", // 5 seconds socket timeout (microseconds) for RTSP
		"-buffer_size", "2048000", // 2MB socket buffer to prevent dropping large video keyframes
		"-probesize", "10000000",  // 10MB probe to ensure video and audio streams are fully analyzed
		"-analyzeduration", "10000000", // 10s analysis duration for slow camera keyframes
		"-fflags", "+genpts+discardcorrupt", // Generate missing PTS and discard corrupt packets
		"-i", rtspURL,
		// Explicit stream mapping: Video (stream 0:v:0) is required, Audio (stream 0:a:0?) is optional
		"-map", "0:v:0",
		"-map", "0:a?",
		// Stream copy for video (zero CPU re-encoding)
		"-c:v", "copy",
		// Encode audio to standard AAC (fixes missing extradata/AudioSpecificConfig from V380 RTSP)
		"-c:a", "aac",
		"-b:a", "64k",
		// Segment muxer
		"-f", "segment",
		"-segment_time", strconv.Itoa(segmentSec),
		"-segment_atclocktime", "1",
		"-reset_timestamps", "1",
		"-strftime", "1",
		"-segment_format", "mp4",
		"-avoid_negative_ts", "make_zero",
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
