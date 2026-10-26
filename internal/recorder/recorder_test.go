package recorder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cctv-monitor/internal/config"
)

func TestGetDiskUsage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_disk_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	usage, err := GetDiskUsage(tmpDir)
	if err != nil {
		t.Fatalf("GetDiskUsage error: %v", err)
	}

	if usage.TotalBytes == 0 {
		t.Errorf("Expected TotalBytes > 0, got %d", usage.TotalBytes)
	}
	if usage.FreeBytes == 0 {
		t.Errorf("Expected FreeBytes > 0, got %d", usage.FreeBytes)
	}
	if usage.TotalFormatted == "" {
		t.Errorf("Expected TotalFormatted not empty")
	}
}

func TestCleanerRetention(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_cleaner_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	camDir := filepath.Join(tmpDir, "cam1")
	if err := os.MkdirAll(camDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Create an old file (> 7 days)
	oldFile := filepath.Join(camDir, "2026-10-01T10-00-00.mp4")
	if err := os.WriteFile(oldFile, []byte("fake old video data"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	// Create a new file (recent)
	newFile := filepath.Join(camDir, "2026-10-26T10-00-00.mp4")
	if err := os.WriteFile(newFile, []byte("fake new video data"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg := &config.Config{
		Recordings: config.RecordingsConfig{
			Dir:           tmpDir,
			RetentionDays: 7,
		},
	}

	cleaner := NewCleaner(cfg)
	cleaner.RunCleanup()

	// oldFile should have been removed
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("Expected oldFile to be deleted, but it exists")
	}

	// newFile should still exist
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("Expected newFile to still exist, but it was deleted")
	}
}

func TestEngineLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_engine_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	recordVal := true
	cfg := &config.Config{
		Recordings: config.RecordingsConfig{
			Enabled:        true,
			Dir:            tmpDir,
			SegmentMinutes: 5,
			RetentionDays:  7,
		},
		Cameras: []config.Camera{
			{
				ID:     "cam1",
				Name:   "Test Cam",
				URL:    "rtsp://invalid-host:554/live",
				Record: &recordVal,
			},
		},
	}

	eng := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := eng.StartAll(ctx); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}

	// Verify process exists
	eng.mu.RLock()
	proc, exists := eng.processes["cam1"]
	eng.mu.RUnlock()
	if !exists || !proc.active {
		t.Errorf("Expected cam1 to have an active record process")
	}

	// StopAll
	eng.StopAll()

	eng.mu.RLock()
	if len(eng.processes) != 0 {
		t.Errorf("Expected 0 processes after StopAll, got %d", len(eng.processes))
	}
	eng.mu.RUnlock()
}
