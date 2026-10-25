package recording

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cctv-monitor/internal/config"
)

// VideoClip represents a single recorded video segment (e.g. 5-minute chunk from Shinobi).
type VideoClip struct {
	ID            string `json:"id"`
	Filename      string `json:"filename"`
	RelativePath  string `json:"relative_path"`
	CameraID      string `json:"camera_id"`
	CameraName    string `json:"camera_name"`
	Date          string `json:"date"`           // YYYY-MM-DD
	Time          string `json:"time"`           // HH:MM:SS
	TimeLabel     string `json:"time_label"`     // HH:MM
	SizeBytes     int64  `json:"size_bytes"`
	SizeFormatted string `json:"size_formatted"`
	StreamURL     string `json:"stream_url"`
}

// CameraRecordingInfo holds information about a camera/monitor that has recordings.
type CameraRecordingInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClipCount int    `json:"clip_count"`
}

// Manager handles scanning, listing, and accessing CCTV recordings from Shinobi or flat directories.
type Manager struct {
	cfg     *config.Config
	baseDir string
	mu      sync.RWMutex
}

// NewManager creates a new recordings Manager.
func NewManager(cfg *config.Config) *Manager {
	dir := cfg.Recordings.Dir
	if dir == "" {
		dir = "./rekaman_cctv"
	}

	return &Manager{
		cfg:     cfg,
		baseDir: filepath.Clean(dir),
	}
}

// BaseDir returns the cleaned base directory for recordings.
func (m *Manager) BaseDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.baseDir
}

// GetCameras returns the list of cameras/monitors found in the recordings storage.
func (m *Manager) GetCameras() []CameraRecordingInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cameraCounts := make(map[string]int)
	m.scanRecordings(func(cameraID string, relPath string, info fs.FileInfo) {
		cameraCounts[cameraID]++
	})

	// Build result with friendly camera names from config if available
	result := make([]CameraRecordingInfo, 0, len(cameraCounts))
	nameMap := make(map[string]string)
	for _, cam := range m.cfg.Cameras {
		nameMap[cam.ID] = cam.Name
	}

	// Sort camera IDs for consistent UI display
	var ids []string
	for id := range cameraCounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		name := id
		if friendly, ok := nameMap[id]; ok {
			name = friendly
		} else if id == "default" {
			if len(m.cfg.Cameras) > 0 {
				name = m.cfg.Cameras[0].Name
			} else {
				name = "Kamera Utama"
			}
		}

		result = append(result, CameraRecordingInfo{
			ID:        id,
			Name:      name,
			ClipCount: cameraCounts[id],
		})
	}

	return result
}

// GetDates returns a sorted list of unique dates (YYYY-MM-DD) that have recordings for the specified camera.
func (m *Manager) GetDates(cameraID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dateSet := make(map[string]bool)

	m.scanRecordings(func(camID string, relPath string, info fs.FileInfo) {
		if cameraID != "" && camID != cameraID && cameraID != "all" {
			return
		}
		date, _, _, ok := parseShinobiFilename(info.Name())
		if ok {
			dateSet[date] = true
		}
	})

	var dates []string
	for d := range dateSet {
		dates = append(dates, d)
	}
	// Sort descending (latest dates first)
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// GetClips returns all recorded clips for a specific camera and date, sorted chronologically.
func (m *Manager) GetClips(cameraID, date string) []VideoClip {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nameMap := make(map[string]string)
	for _, cam := range m.cfg.Cameras {
		nameMap[cam.ID] = cam.Name
	}

	var clips []VideoClip

	m.scanRecordings(func(camID string, relPath string, info fs.FileInfo) {
		if cameraID != "" && camID != cameraID && cameraID != "all" {
			return
		}

		fileDate, timeStr, timeLabel, ok := parseShinobiFilename(info.Name())
		if !ok {
			return
		}

		if date != "" && fileDate != date {
			return
		}

		camName := camID
		if friendly, exists := nameMap[camID]; exists {
			camName = friendly
		} else if camID == "default" {
			if len(m.cfg.Cameras) > 0 {
				camName = m.cfg.Cameras[0].Name
			} else {
				camName = "Kamera Utama"
			}
		}

		// Normalize relative path with forward slashes for URL
		urlRelPath := filepath.ToSlash(relPath)

		clips = append(clips, VideoClip{
			ID:            info.Name(),
			Filename:      info.Name(),
			RelativePath:  urlRelPath,
			CameraID:      camID,
			CameraName:    camName,
			Date:          fileDate,
			Time:          timeStr,
			TimeLabel:     timeLabel,
			SizeBytes:     info.Size(),
			SizeFormatted: formatBytes(info.Size()),
			StreamURL:     "/api/recordings/stream?file=" + urlRelPath,
		})
	})

	// Sort clips chronologically by Date + Time
	sort.Slice(clips, func(i, j int) bool {
		if clips[i].Date != clips[j].Date {
			return clips[i].Date < clips[j].Date
		}
		return clips[i].Time < clips[j].Time
	})

	return clips
}

// GetFullPath safely resolves and validates a relative recording path against baseDir.
// It returns an error if directory traversal is detected or the file does not exist.
func (m *Manager) GetFullPath(relativePath string) (string, error) {
	m.mu.RLock()
	base := m.baseDir
	m.mu.RUnlock()

	// Clean path and prevent directory traversal
	cleanRel := filepath.Clean(filepath.FromSlash(relativePath))
	if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("invalid file path: directory traversal attempt")
	}

	fullPath := filepath.Join(base, cleanRel)

	// Ensure the fullPath is strictly inside base
	relCheck, err := filepath.Rel(base, fullPath)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("access denied: path outside recordings directory")
	}

	stat, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}
	if stat.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}

	return fullPath, nil
}

// scanRecordings walks the base directory and identifies camera IDs and video files.
// Supports both:
// 1. Flat folder: baseDir/*.mp4 -> cameraID = "default"
// 2. Nested monitor folder: baseDir/{monitorID}/*.mp4 -> cameraID = monitorID
// 3. Shinobi hierarchy: baseDir/{groupKey}/{monitorID}/*.mp4 -> cameraID = monitorID
func (m *Manager) scanRecordings(fn func(cameraID string, relPath string, info fs.FileInfo)) {
	if _, err := os.Stat(m.baseDir); os.IsNotExist(err) {
		return
	}

	_ = filepath.WalkDir(m.baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			// Skip internal folders
			if name == "lost+found" || name == "temp" || strings.HasSuffix(name, "_timelapse") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(strings.ToLower(d.Name()), ".mp4") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(m.baseDir, path)
		if err != nil {
			return nil
		}

		// Determine camera ID from path
		dirParts := strings.Split(filepath.ToSlash(filepath.Dir(relPath)), "/")
		cameraID := "default"

		if len(dirParts) == 1 && dirParts[0] != "." && dirParts[0] != "" {
			// e.g. "cam1/video.mp4" or "ztIRNMjufs/video.mp4"
			cameraID = dirParts[0]
		} else if len(dirParts) >= 2 {
			// e.g. "fM8ybTrNUO/ztIRNMjufs/video.mp4" -> monitorId is dirParts[1]
			cameraID = dirParts[len(dirParts)-1]
		}

		fn(cameraID, relPath, info)
		return nil
	})
}

// parseShinobiFilename extracts date and time from Shinobi filenames like "2026-10-24T13-40-00.mp4".
func parseShinobiFilename(name string) (dateStr, timeStr, timeLabel string, ok bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))

	// Match Shinobi pattern YYYY-MM-DDTHH-mm-ss
	parts := strings.Split(base, "T")
	if len(parts) < 2 {
		return "", "", "", false
	}

	datePart := parts[0]
	if len(datePart) != 10 || datePart[4] != '-' || datePart[7] != '-' {
		return "", "", "", false
	}

	// Validate date values
	if _, err := time.Parse("2006-01-02", datePart); err != nil {
		return "", "", "", false
	}

	timePart := parts[1]
	if len(timePart) < 8 {
		return "", "", "", false
	}

	// Extract HH, mm, ss
	hh := timePart[0:2]
	mm := timePart[3:5]
	ss := timePart[6:8]

	formattedTime := fmt.Sprintf("%s:%s:%s", hh, mm, ss)
	label := fmt.Sprintf("%s:%s", hh, mm)

	return datePart, formattedTime, label, true
}

// formatBytes formats byte sizes into readable string representations.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
