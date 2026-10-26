package stream

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/onvif"
)

// CameraStatus represents the current state of a camera stream.
type CameraStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // "online", "connecting", "offline", "error"
	Error  string `json:"error,omitempty"`
	HlsURL string `json:"hls_url"`
}

// cameraProcess tracks an individual camera's FFmpeg process and state.
type cameraProcess struct {
	camera     config.Camera
	loopCancel context.CancelFunc
	ffCancel   context.CancelFunc
	status     string
	errMsg     string
	mu         sync.RWMutex
}

func (p *cameraProcess) setStatus(status, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
	p.errMsg = errMsg
}

func (p *cameraProcess) getStatus() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status, p.errMsg
}

// Manager manages FFmpeg processes for all configured cameras.
type Manager struct {
	cfg       *config.Config
	processes map[string]*cameraProcess
	parentCtx context.Context
	mu        sync.RWMutex
}

// NewManager creates a new stream manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:       cfg,
		processes: make(map[string]*cameraProcess),
	}
}

// StartAll launches FFmpeg streaming goroutines for all configured cameras.
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	m.parentCtx = ctx
	m.mu.Unlock()

	// Create base streams directory
	if err := os.MkdirAll(m.cfg.Server.StreamsDir, 0755); err != nil {
		return fmt.Errorf("create streams dir: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cam := range m.cfg.Cameras {
		camDir := filepath.Join(m.cfg.Server.StreamsDir, cam.ID)
		if err := os.MkdirAll(camDir, 0755); err != nil {
			return fmt.Errorf("create dir for camera %q: %w", cam.ID, err)
		}
		m.startCameraLocked(ctx, cam)
	}

	return nil
}

// AddCamera registers a new camera, persists stream dir, and launches streaming goroutines.
func (m *Manager) AddCamera(ctx context.Context, cam config.Camera) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.cfg.Cameras {
		if c.ID == cam.ID {
			return fmt.Errorf("kamera dengan ID %q sudah ada", cam.ID)
		}
	}

	camDir := filepath.Join(m.cfg.Server.StreamsDir, cam.ID)
	if err := os.MkdirAll(camDir, 0755); err != nil {
		return fmt.Errorf("create dir for camera %q: %w", cam.ID, err)
	}

	m.cfg.Cameras = append(m.cfg.Cameras, cam)
	m.startCameraLocked(ctx, cam)
	return nil
}

// UpdateCamera updates a camera configuration and restarts its stream process.
func (m *Manager) UpdateCamera(ctx context.Context, cam config.Camera) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	for i, c := range m.cfg.Cameras {
		if c.ID == cam.ID {
			m.cfg.Cameras[i] = cam
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("kamera %q tidak ditemukan", cam.ID)
	}

	camDir := filepath.Join(m.cfg.Server.StreamsDir, cam.ID)
	_ = os.MkdirAll(camDir, 0755)

	m.stopCameraLocked(cam.ID)
	m.startCameraLocked(ctx, cam)
	return nil
}

// RemoveCamera stops and removes a camera from the manager.
func (m *Manager) RemoveCamera(camID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	newCams := make([]config.Camera, 0, len(m.cfg.Cameras))
	for _, c := range m.cfg.Cameras {
		if c.ID == camID {
			found = true
			continue
		}
		newCams = append(newCams, c)
	}
	if !found {
		return fmt.Errorf("kamera %q tidak ditemukan", camID)
	}
	m.cfg.Cameras = newCams

	m.stopCameraLocked(camID)

	// Clean up stream directory
	camDir := filepath.Join(m.cfg.Server.StreamsDir, camID)
	_ = os.RemoveAll(camDir)

	return nil
}

func (m *Manager) startCameraLocked(ctx context.Context, cam config.Camera) {
	if ctx == nil {
		ctx = m.parentCtx
	}
	if ctx == nil {
		ctx = context.Background()
	}

	camCtx, loopCancel := context.WithCancel(ctx)
	proc := &cameraProcess{
		camera:     cam,
		loopCancel: loopCancel,
		status:     "connecting",
	}
	m.processes[cam.ID] = proc

	go m.runCameraLoop(camCtx, proc)
	go m.thumbnailLoop(camCtx, proc)
}

func (m *Manager) stopCameraLocked(camID string) {
	if proc, ok := m.processes[camID]; ok {
		proc.mu.Lock()
		if proc.ffCancel != nil {
			proc.ffCancel()
		}
		if proc.loopCancel != nil {
			proc.loopCancel()
		}
		proc.status = "offline"
		proc.mu.Unlock()
		delete(m.processes, camID)
		log.Printf("[%s] Stream stopped and removed", camID)
	}
}

// runCameraLoop is the main loop for a single camera.
// It resolves the RTSP URL, starts FFmpeg, and auto-reconnects on failure.
func (m *Manager) runCameraLoop(ctx context.Context, proc *cameraProcess) {
	camID := proc.camera.ID

	for {
		select {
		case <-ctx.Done():
			proc.setStatus("offline", "")
			return
		default:
		}

		proc.setStatus("connecting", "")

		// --- Step 1: Resolve RTSP URL ---
		rtspURL := proc.camera.URL

		if rtspURL == "" && proc.camera.OnvifURL != "" {
			log.Printf("[%s] Discovering RTSP URL via ONVIF at %s...", camID, proc.camera.OnvifURL)
			discovered, err := onvif.GetStreamURI(proc.camera.OnvifURL, proc.camera.Username, proc.camera.Password)
			if err != nil {
				log.Printf("[%s] ONVIF discovery failed: %v", camID, err)
				proc.setStatus("error", fmt.Sprintf("ONVIF discovery failed: %v", err))
				sleepOrCancel(ctx, 10*time.Second)
				continue
			}
			rtspURL = discovered
			log.Printf("[%s] ONVIF discovered RTSP URL: %s", camID, maskPassword(rtspURL))
		}

		if rtspURL == "" {
			proc.setStatus("error", "no RTSP URL configured")
			sleepOrCancel(ctx, 30*time.Second)
			continue
		}

		// --- Step 2: Start FFmpeg ---
		log.Printf("[%s] Starting FFmpeg stream...", camID)
		outPath := filepath.Join(m.cfg.Server.StreamsDir, camID, "index.m3u8")

		ffCtx, ffCancel := context.WithCancel(ctx)
		proc.mu.Lock()
		proc.ffCancel = ffCancel
		proc.mu.Unlock()

		err := m.runFFmpeg(ffCtx, camID, rtspURL, outPath, proc)
		ffCancel()

		// Check if shutdown was requested
		if ctx.Err() != nil {
			proc.setStatus("offline", "")
			return
		}

		// FFmpeg exited unexpectedly — reconnect
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		log.Printf("[%s] FFmpeg exited: %v — reconnecting in 5s...", camID, err)
		proc.setStatus("connecting", fmt.Sprintf("reconnecting: %s", errStr))

		sleepOrCancel(ctx, 5*time.Second)
	}
}

// runFFmpeg starts an FFmpeg process that converts RTSP to HLS.
// It uses -c:v copy (no re-encoding) for minimal CPU usage.
func (m *Manager) runFFmpeg(ctx context.Context, camID, rtspURL, outPath string, proc *cameraProcess) error {
	segPattern := filepath.Join(filepath.Dir(outPath), "seg_%03d.ts")

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		// Input options
		"-rtsp_transport", "tcp",
		"-timeout", "5000000", // 5 seconds connection timeout (microseconds)
		"-i", rtspURL,
		// Output options — copy codec, no re-encoding
		"-c:v", "copy",
		"-an", // Disable audio (remove this line if you want audio)
		// HLS output
		"-f", "hls",
		"-hls_time", "2",      // 2-second segments
		"-hls_list_size", "3", // Keep only 3 segments in the playlist
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", segPattern,
		outPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr // Show FFmpeg warnings/errors in server log

	log.Printf("[%s] FFmpeg command: ffmpeg %s", camID, strings.Join(maskArgs(args), " "))

	if err := cmd.Start(); err != nil {
		proc.setStatus("error", fmt.Sprintf("FFmpeg start failed: %v", err))
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// After 3 seconds, if FFmpeg is still running, mark camera as online
	go func() {
		select {
		case <-time.After(3 * time.Second):
			if ctx.Err() == nil {
				s, _ := proc.getStatus()
				if s == "connecting" {
					proc.setStatus("online", "")
					log.Printf("[%s] Stream is ONLINE", camID)
				}
			}
		case <-ctx.Done():
		}
	}()

	return cmd.Wait()
}

// StopAll terminates all running FFmpeg processes.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, proc := range m.processes {
		proc.mu.Lock()
		if proc.ffCancel != nil {
			proc.ffCancel()
		}
		if proc.loopCancel != nil {
			proc.loopCancel()
		}
		proc.status = "offline"
		proc.mu.Unlock()
		log.Printf("[%s] Stream stopped", id)
	}
	m.processes = make(map[string]*cameraProcess)
}

// GetCameras returns the current status of all cameras, maintaining config order.
func (m *Manager) GetCameras() []CameraStatus {
	cameras := make([]CameraStatus, 0, len(m.cfg.Cameras))

	for _, cam := range m.cfg.Cameras {
		status := CameraStatus{
			ID:     cam.ID,
			Name:   cam.Name,
			Status: "offline",
			HlsURL: fmt.Sprintf("/streams/%s/index.m3u8", cam.ID),
		}

		m.mu.RLock()
		proc, ok := m.processes[cam.ID]
		m.mu.RUnlock()

		if ok {
			s, e := proc.getStatus()
			status.Status = s
			status.Error = e
		}

		cameras = append(cameras, status)
	}

	return cameras
}

// StreamsDir returns the base directory where HLS segments are stored.
func (m *Manager) StreamsDir() string {
	return m.cfg.Server.StreamsDir
}

// GetCamera returns the camera configuration for the given ID.
func (m *Manager) GetCamera(id string) (config.Camera, bool) {
	for _, cam := range m.cfg.Cameras {
		if cam.ID == id {
			return cam, true
		}
	}
	return config.Camera{}, false
}

// thumbnailLoop periodically extracts a JPEG thumbnail from the latest completed HLS segment.
// This runs independently from the main stream loop.
// The thumbnail is saved as thumbnail.jpg in the camera's stream directory atomically.
func (m *Manager) thumbnailLoop(ctx context.Context, proc *cameraProcess) {
	camID := proc.camera.ID
	dir := filepath.Join(m.cfg.Server.StreamsDir, camID)
	thumbPath := filepath.Join(dir, "thumbnail.jpg")
	tempThumbPath := filepath.Join(dir, "thumbnail.tmp.jpg")

	// Initial delay to let FFmpeg produce the first segments
	sleepOrCancel(ctx, 3*time.Second)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, _ := proc.getStatus()
			if status != "online" {
				continue
			}

			// Find .ts segment files
			segments, err := filepath.Glob(filepath.Join(dir, "seg_*.ts"))
			if err != nil || len(segments) == 0 {
				continue
			}
			sort.Strings(segments)

			// Pick completed segment (avoiding the one currently being written by FFmpeg)
			target := segments[len(segments)-1]
			if len(segments) >= 2 {
				target = segments[len(segments)-2]
			}

			// Extract 1 frame as JPEG to a temp file, then atomic rename
			cmd := exec.CommandContext(ctx, "ffmpeg",
				"-hide_banner", "-loglevel", "error",
				"-i", target,
				"-frames:v", "1",
				"-q:v", "5",
				"-y",
				tempThumbPath,
			)
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Run(); err != nil {
				log.Printf("[%s] Thumbnail extraction failed: %v", camID, err)
			} else {
				_ = os.Rename(tempThumbPath, thumbPath)
			}
		}
	}
}

// --- Helpers ---

// sleepOrCancel sleeps for the given duration, but returns early if context is cancelled.
func sleepOrCancel(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// maskPassword hides password in RTSP URLs for logging.
// Example: rtsp://admin:secret@192.168.1.1 → rtsp://admin:***@192.168.1.1
func maskPassword(url string) string {
	if idx := strings.Index(url, "@"); idx > 0 {
		prefix := url[:strings.Index(url, "://")+3]
		hostPart := url[idx:]
		userPart := url[len(prefix):idx]
		if colonIdx := strings.Index(userPart, ":"); colonIdx > 0 {
			return prefix + userPart[:colonIdx] + ":***" + hostPart
		}
	}
	return url
}

// maskArgs hides passwords in FFmpeg arguments for logging.
func maskArgs(args []string) []string {
	masked := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "rtsp://") {
			masked[i] = maskPassword(a)
		} else {
			masked[i] = a
		}
	}
	return masked
}
