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
	"cctv-monitor/internal/motion"
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
	HasMotion     bool   `json:"has_motion"`
}

// CameraRecordingInfo holds information about a camera/monitor that has recordings.
type CameraRecordingInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClipCount int    `json:"clip_count"`
}

// Manager handles scanning, listing, and accessing CCTV recordings from Shinobi or flat directories.
type Manager struct {
	cfg       *config.Config
	baseDir   string
	tzLoc     *time.Location
	motionMgr *motion.EventManager
	mu        sync.RWMutex
}

// SetEventManager sets the motion event manager for tagging clips with motion events.
func (m *Manager) SetEventManager(mem *motion.EventManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.motionMgr = mem
}

// NewManager creates a new recordings Manager.
func NewManager(cfg *config.Config) *Manager {
	dir := cfg.Recordings.Dir
	if dir == "" {
		dir = "./rekaman_cctv"
	}

	offset := cfg.Recordings.TimezoneOffsetHours
	if offset == 0 {
		offset = 7 // Default UTC+7 (WIB)
	}
	loc := time.FixedZone(fmt.Sprintf("UTC%+d", offset), offset*3600)

	return &Manager{
		cfg:     cfg,
		baseDir: filepath.Clean(dir),
		tzLoc:   loc,
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

	result := make([]CameraRecordingInfo, 0)
	handledIDs := make(map[string]bool)

	// 1. First, prioritize and order by cameras defined in config.yaml
	for _, cam := range m.cfg.Cameras {
		matchedID := ""
		count := 0

		if cam.ShinobiID != "" && cameraCounts[cam.ShinobiID] > 0 {
			matchedID = cam.ShinobiID
			count = cameraCounts[cam.ShinobiID]
		} else if cameraCounts[cam.ID] > 0 {
			matchedID = cam.ID
			count = cameraCounts[cam.ID]
		} else if cameraCounts["default"] > 0 && len(m.cfg.Cameras) == 1 {
			matchedID = "default"
			count = cameraCounts["default"]
		} else if len(m.cfg.Cameras) == 1 && len(cameraCounts) == 1 {
			// Single camera fallback
			for id, c := range cameraCounts {
				matchedID = id
				count = c
				break
			}
		}

		if matchedID != "" && !handledIDs[matchedID] {
			handledIDs[matchedID] = true
			result = append(result, CameraRecordingInfo{
				ID:        matchedID,
				Name:      cam.Name,
				ClipCount: count,
			})
		}
	}

	// 2. If no configured camera matched, fallback to listing detected camera IDs
	if len(result) == 0 {
		var ids []string
		for id := range cameraCounts {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		for _, id := range ids {
			name := id
			if id == "default" && len(m.cfg.Cameras) > 0 {
				name = m.cfg.Cameras[0].Name
			}
			result = append(result, CameraRecordingInfo{
				ID:        id,
				Name:      name,
				ClipCount: cameraCounts[id],
			})
		}
	}

	return result
}

// matchesCamera checks if a target camera ID matches the scanned camera ID,
// taking into account camera ID, ShinobiID mapping, or single-camera fallback.
func (m *Manager) matchesCamera(targetCamID, scannedCamID string) bool {
	if targetCamID == "" || targetCamID == "all" || targetCamID == scannedCamID {
		return true
	}
	for _, cam := range m.cfg.Cameras {
		if cam.ID == targetCamID && cam.ShinobiID == scannedCamID {
			return true
		}
		if cam.ShinobiID == targetCamID && cam.ID == scannedCamID {
			return true
		}
	}
	if len(m.cfg.Cameras) == 1 && (targetCamID == m.cfg.Cameras[0].ID || targetCamID == "default") {
		return true
	}
	return false
}

// GetDates returns a sorted list of unique dates (YYYY-MM-DD) that have recordings for the specified camera.
func (m *Manager) GetDates(cameraID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dateSet := make(map[string]bool)

	m.scanRecordings(func(camID string, relPath string, info fs.FileInfo) {
		if !m.matchesCamera(cameraID, camID) {
			return
		}
		date, _, _, ok := m.parseShinobiFilename(info.Name())
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
		if cam.ShinobiID != "" {
			nameMap[cam.ShinobiID] = cam.Name
		}
	}

	var clips []VideoClip

	m.scanRecordings(func(camID string, relPath string, info fs.FileInfo) {
		if !m.matchesCamera(cameraID, camID) {
			return
		}

		fileDate, timeStr, timeLabel, ok := m.parseShinobiFilename(info.Name())
		if !ok {
			return
		}

		if date != "" && fileDate != date {
			return
		}

		camName := camID
		if friendly, exists := nameMap[camID]; exists {
			camName = friendly
		} else if camID == "default" || len(m.cfg.Cameras) == 1 {
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

	// If motion event manager is available, tag clips that have overlapping motion events
	if m.motionMgr != nil && len(clips) > 0 {
		eventsCache := make(map[string][]motion.Event)
		segMin := m.cfg.Recordings.SegmentMinutes
		if segMin <= 0 {
			segMin = 5
		}
		segSec := segMin * 60

		for i := range clips {
			clip := &clips[i]
			targetCam := clip.CameraID
			cacheKey := targetCam + "_" + clip.Date
			events, ok := eventsCache[cacheKey]
			if !ok {
				events = m.motionMgr.GetEvents(targetCam, clip.Date)
				if len(events) == 0 && cameraID != "" && cameraID != "all" && cameraID != targetCam {
					events = m.motionMgr.GetEvents(cameraID, clip.Date)
				}
				eventsCache[cacheKey] = events
			}

			if len(events) == 0 {
				continue
			}

			var h, min, s int
			n, _ := fmt.Sscanf(clip.Time, "%d:%d:%d", &h, &min, &s)
			if n >= 2 {
				clipStart := h*3600 + min*60 + s
				clipEnd := clipStart + segSec
				for _, ev := range events {
					if ev.EndSec >= clipStart && ev.StartSec <= clipEnd {
						clip.HasMotion = true
						break
					}
				}
			}
		}
	}

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

		// Only include files with valid Shinobi timestamp filenames (YYYY-MM-DDTHH-mm-ss.mp4)
		if _, _, _, ok := parseShinobiFilename(d.Name()); !ok {
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
// Shinobi records timestamps in UTC (GMT+0). This converts them to the target timezone (e.g. UTC+7 / WIB).
func (m *Manager) parseShinobiFilename(name string) (dateStr, timeStr, timeLabel string, ok bool) {
	return parseShinobiFilenameWithLocation(name, m.tzLoc)
}

// parseShinobiFilename extracts date and time using default UTC+7 if called without manager instance.
func parseShinobiFilename(name string) (dateStr, timeStr, timeLabel string, ok bool) {
	loc := time.FixedZone("WIB", 7*3600)
	return parseShinobiFilenameWithLocation(name, loc)
}

func parseShinobiFilenameWithLocation(name string, loc *time.Location) (dateStr, timeStr, timeLabel string, ok bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))

	// Match Shinobi pattern YYYY-MM-DDTHH-mm-ss
	parts := strings.Split(base, "T")
	if len(parts) < 2 {
		return "", "", "", false
	}

	datePart := parts[0]
	timePart := parts[1]

	if len(datePart) != 10 || len(timePart) < 8 {
		return "", "", "", false
	}

	// Reconstruct standard ISO timestamp: YYYY-MM-DDTHH:mm:ss
	isoStr := fmt.Sprintf("%sT%s:%s:%s", datePart, timePart[0:2], timePart[3:5], timePart[6:8])

	// Shinobi names files in UTC
	utcTime, err := time.ParseInLocation("2006-01-02T15:04:05", isoStr, time.UTC)
	if err != nil {
		return "", "", "", false
	}

	// Convert to target timezone (e.g. UTC+7)
	targetTime := utcTime
	if loc != nil {
		targetTime = utcTime.In(loc)
	}

	return targetTime.Format("2006-01-02"), targetTime.Format("15:04:05"), targetTime.Format("15:04"), true
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
