package motion

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cctv-monitor/internal/config"
)

// Event represents a continuous interval of detected motion.
type Event struct {
	CameraID     string    `json:"camera_id"`
	Date         string    `json:"date"`       // YYYY-MM-DD
	StartTime    string    `json:"start_time"` // HH:MM:SS
	EndTime      string    `json:"end_time"`   // HH:MM:SS
	StartSec     int       `json:"start_sec"`  // Seconds from 00:00:00
	EndSec       int       `json:"end_sec"`    // Seconds from 00:00:00
	MaxChangePct float64   `json:"max_change_pct"`
	LastSeen     time.Time `json:"-"`
}

// EventManager handles persisting and querying motion detection events.
type EventManager struct {
	cfg          *config.Config
	tzLoc        *time.Location
	activeEvents map[string]*Event // camID -> active ongoing event
	cache        map[string][]Event // "camID:date" -> cached events
	mu           sync.RWMutex
}

// NewEventManager creates a new motion event manager.
func NewEventManager(cfg *config.Config) *EventManager {
	offset := cfg.Recordings.TimezoneOffsetHours
	if offset == 0 {
		offset = 7 // Default UTC+7 (WIB)
	}
	loc := time.FixedZone(fmt.Sprintf("UTC%+d", offset), offset*3600)

	return &EventManager{
		cfg:          cfg,
		tzLoc:        loc,
		activeEvents: make(map[string]*Event),
		cache:        make(map[string][]Event),
	}
}

// RecordMotion records a motion trigger at time t. If an active event exists within 8 seconds, it extends it.
func (em *EventManager) RecordMotion(camID string, t time.Time, changePct float64) {
	em.mu.Lock()
	defer em.mu.Unlock()

	localTime := t.In(em.tzLoc)
	dateStr := localTime.Format("2006-01-02")
	timeStr := localTime.Format("15:04:05")
	sec := localTime.Hour()*3600 + localTime.Minute()*60 + localTime.Second()

	active, exists := em.activeEvents[camID]
	if exists && active.Date == dateStr && t.Sub(active.LastSeen) <= 8*time.Second {
		// Extend active event
		active.EndTime = timeStr
		active.EndSec = sec
		active.LastSeen = t
		if changePct > active.MaxChangePct {
			active.MaxChangePct = changePct
		}
		// Persist update
		em.saveActiveEventLocked(camID, active)
		return
	}

	// Start new event
	event := &Event{
		CameraID:     camID,
		Date:         dateStr,
		StartTime:    timeStr,
		EndTime:      timeStr,
		StartSec:     sec,
		EndSec:       sec,
		MaxChangePct: changePct,
		LastSeen:     t,
	}
	em.activeEvents[camID] = event

	// Persist
	em.appendEventLocked(camID, event)
	log.Printf("[MOTION][%s] Gerakan terdeteksi pada %s (perubahan visual: %.1f%%)", camID, timeStr, changePct)
}

// GetEvents returns all motion events for the specified camera and date (YYYY-MM-DD).
func (em *EventManager) GetEvents(camID, date string) []Event {
	localToday := time.Now().In(em.tzLoc).Format("2006-01-02")
	isToday := (date == localToday)

	em.mu.RLock()
	cacheKey := fmt.Sprintf("%s:%s", camID, date)
	if events, ok := em.cache[cacheKey]; ok && !isToday {
		em.mu.RUnlock()
		return events
	}
	em.mu.RUnlock()

	// Load from disk with alias and single-camera resolution
	events := em.loadEventsFromDisk(camID, date)

	em.mu.Lock()
	em.cache[cacheKey] = events
	em.mu.Unlock()

	return events
}

func (em *EventManager) getEventFilePath(camID, date string) string {
	baseDir := em.cfg.Recordings.Dir
	if baseDir == "" {
		baseDir = "./rekaman_cctv"
	}
	return filepath.Join(baseDir, camID, fmt.Sprintf("events_%s.json", date))
}

func readEventsFile(path string) []Event {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil
	}
	return events
}

func (em *EventManager) loadEventsFromDisk(camID, date string) []Event {
	baseDir := em.cfg.Recordings.Dir
	if baseDir == "" {
		baseDir = "./rekaman_cctv"
	}

	// 1. Direct path: baseDir/camID/events_date.json
	path := em.getEventFilePath(camID, date)
	if events := readEventsFile(path); len(events) > 0 {
		return events
	}

	// 2. Root path: baseDir/events_date.json
	rootPath := filepath.Join(baseDir, fmt.Sprintf("events_%s.json", date))
	if events := readEventsFile(rootPath); len(events) > 0 {
		return events
	}

	// 3. Check configured camera mappings (ID <-> ShinobiID)
	if em.cfg != nil {
		for _, cam := range em.cfg.Cameras {
			if cam.ID == camID && cam.ShinobiID != "" {
				p := filepath.Join(baseDir, cam.ShinobiID, fmt.Sprintf("events_%s.json", date))
				if events := readEventsFile(p); len(events) > 0 {
					return events
				}
			}
			if cam.ShinobiID == camID {
				p := filepath.Join(baseDir, cam.ID, fmt.Sprintf("events_%s.json", date))
				if events := readEventsFile(p); len(events) > 0 {
					return events
				}
			}
		}

		// 4. Single camera fallback
		if len(em.cfg.Cameras) == 1 {
			primaryCam := em.cfg.Cameras[0]
			candidates := []string{primaryCam.ID, primaryCam.ShinobiID, "default"}
			for _, cid := range candidates {
				if cid == "" || cid == camID {
					continue
				}
				p := filepath.Join(baseDir, cid, fmt.Sprintf("events_%s.json", date))
				if events := readEventsFile(p); len(events) > 0 {
					return events
				}
			}
		}
	}

	// 5. Fallback search: any subfolder in baseDir for events_date.json
	matches, _ := filepath.Glob(filepath.Join(baseDir, "*", fmt.Sprintf("events_%s.json", date)))
	for _, m := range matches {
		if events := readEventsFile(m); len(events) > 0 {
			return events
		}
	}

	return []Event{}
}

func (em *EventManager) appendEventLocked(camID string, event *Event) {
	events := em.loadEventsFromDisk(camID, event.Date)
	events = append(events, *event)
	// Reset cache so queries with alias IDs get updated
	em.cache = make(map[string][]Event)
	em.cache[fmt.Sprintf("%s:%s", camID, event.Date)] = events

	em.writeEventsToDisk(camID, event.Date, events)
}

func (em *EventManager) saveActiveEventLocked(camID string, active *Event) {
	events := em.loadEventsFromDisk(camID, active.Date)
	if len(events) > 0 {
		// Update last element
		events[len(events)-1] = *active
	} else {
		events = append(events, *active)
	}
	em.cache = make(map[string][]Event)
	em.cache[fmt.Sprintf("%s:%s", camID, active.Date)] = events

	em.writeEventsToDisk(camID, active.Date, events)
}

func (em *EventManager) writeEventsToDisk(camID, date string, events []Event) {
	filePath := em.getEventFilePath(camID, date)
	dir := filepath.Dir(filePath)
	_ = os.MkdirAll(dir, 0755)

	data, err := json.Marshal(events)
	if err != nil {
		return
	}

	// Atomic write
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err == nil {
		_ = os.Rename(tmpFile, filePath)
	}
}
