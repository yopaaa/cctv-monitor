package recording

import (
	"os"
	"path/filepath"
	"testing"

	"cctv-monitor/internal/config"
)

func TestRecordingManager(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_rec_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	camDir := filepath.Join(tmpDir, "cam1")
	if err := os.MkdirAll(camDir, 0755); err != nil {
		t.Fatalf("failed to create cam dir: %v", err)
	}

	testFiles := []string{
		"2026-10-24T13-40-00.mp4",
		"2026-10-24T13-45-01.mp4",
		"2026-10-25T08-00-00.mp4",
	}

	for _, f := range testFiles {
		p := filepath.Join(camDir, f)
		if err := os.WriteFile(p, []byte("fake mp4 content"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", f, err)
		}
	}

	cfg := &config.Config{
		Recordings: config.RecordingsConfig{
			Dir: tmpDir,
		},
		Cameras: []config.Camera{
			{ID: "cam1", Name: "Kamera Depan"},
		},
	}

	mgr := NewManager(cfg)

	// Test GetCameras
	cameras := mgr.GetCameras()
	if len(cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cameras))
	}
	if cameras[0].ID != "cam1" || cameras[0].Name != "Kamera Depan" {
		t.Errorf("unexpected camera info: %+v", cameras[0])
	}
	if cameras[0].ClipCount != 3 {
		t.Errorf("expected 3 clips, got %d", cameras[0].ClipCount)
	}

	// Test GetDates
	dates := mgr.GetDates("cam1")
	if len(dates) != 2 {
		t.Fatalf("expected 2 dates, got %d", len(dates))
	}
	// Sorted descending: 2026-10-25 then 2026-10-24
	if dates[0] != "2026-10-25" || dates[1] != "2026-10-24" {
		t.Errorf("unexpected dates: %v", dates)
	}

	// Test GetClips
	clips := mgr.GetClips("cam1", "2026-10-24")
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips for 2026-10-24, got %d", len(clips))
	}
	if clips[0].TimeLabel != "13:40" || clips[1].TimeLabel != "13:45" {
		t.Errorf("unexpected clip ordering: %s, %s", clips[0].TimeLabel, clips[1].TimeLabel)
	}

	// Test GetFullPath
	fullPath, err := mgr.GetFullPath("cam1/2026-10-24T13-40-00.mp4")
	if err != nil {
		t.Fatalf("failed to get full path: %v", err)
	}
	if fullPath != filepath.Join(camDir, "2026-10-24T13-40-00.mp4") {
		t.Errorf("unexpected full path: %s", fullPath)
	}

	// Test Traversal prevention
	_, err = mgr.GetFullPath("../../etc/passwd")
	if err == nil {
		t.Errorf("expected error on directory traversal attempt, got nil")
	}
}
