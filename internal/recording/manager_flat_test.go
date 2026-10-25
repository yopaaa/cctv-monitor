package recording

import (
	"os"
	"path/filepath"
	"testing"

	"cctv-monitor/internal/config"
)

func TestRecordingManagerFlatDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_flat_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files directly inside tmpDir (flat)
	testFiles := []string{
		"2026-10-24T13-40-00.mp4",
		"2026-10-24T13-45-01.mp4",
	}

	for _, f := range testFiles {
		p := filepath.Join(tmpDir, f)
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

	// Test GetCameras - should detect "default" mapped to "Kamera Depan"
	cameras := mgr.GetCameras()
	if len(cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cameras))
	}
	if cameras[0].ID != "default" || cameras[0].Name != "Kamera Depan" {
		t.Errorf("unexpected camera info: %+v", cameras[0])
	}
	if cameras[0].ClipCount != 2 {
		t.Errorf("expected 2 clips, got %d", cameras[0].ClipCount)
	}

	// Test GetDates
	dates := mgr.GetDates("default")
	if len(dates) != 1 || dates[0] != "2026-10-24" {
		t.Fatalf("expected ['2026-10-24'], got %v", dates)
	}

	// Test GetClips
	clips := mgr.GetClips("default", "2026-10-24")
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips for 2026-10-24, got %d", len(clips))
	}
	if clips[0].StreamURL != "/api/recordings/stream?file=2026-10-24T13-40-00.mp4" {
		t.Errorf("unexpected stream URL: %s", clips[0].StreamURL)
	}

	// Test GetFullPath
	fullPath, err := mgr.GetFullPath("2026-10-24T13-40-00.mp4")
	if err != nil {
		t.Fatalf("failed to get full path: %v", err)
	}
	if fullPath != filepath.Join(tmpDir, "2026-10-24T13-40-00.mp4") {
		t.Errorf("unexpected full path: %s", fullPath)
	}
}
