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
	if clips[0].TimeLabel != "20:40" || clips[1].TimeLabel != "20:45" {
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

func TestShinobiHierarchicalStructure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_shinobi_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create Shinobi folder structure: {groupKey}/{monitorID}
	// E.g. fM8ybTrNUO/ztIRNMjufs
	groupKey := "fM8ybTrNUO"
	monitorID := "ztIRNMjufs"
	recDir := filepath.Join(tmpDir, groupKey, monitorID)
	if err := os.MkdirAll(recDir, 0755); err != nil {
		t.Fatalf("failed to create shinobi dir: %v", err)
	}

	// Create ignored folders to verify filtering
	_ = os.MkdirAll(filepath.Join(tmpDir, "lost+found"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "temp"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, groupKey, monitorID+"_timelapse"), 0755)

	// Create random/test directory like "tes-rekam" with a dummy mp4
	tesRekamDir := filepath.Join(tmpDir, "tes-rekam")
	_ = os.MkdirAll(tesRekamDir, 0755)
	_ = os.WriteFile(filepath.Join(tesRekamDir, "dummy_test.mp4"), []byte("non-shinobi video"), 0644)

	testFiles := []string{
		"2026-10-18T12-50-01.mp4",
		"2026-10-18T12-55-00.mp4",
		"2026-10-20T06-35-01.mp4",
	}

	for _, f := range testFiles {
		p := filepath.Join(recDir, f)
		if err := os.WriteFile(p, []byte("fake mp4 content"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", f, err)
		}
	}

	cfg := &config.Config{
		Recordings: config.RecordingsConfig{
			Dir: tmpDir,
		},
		Cameras: []config.Camera{
			{ID: "cam1", Name: "Kamera Depan", ShinobiID: "ztIRNMjufs"},
		},
	}

	mgr := NewManager(cfg)

	// Verify GetCameras returns friendly name from ShinobiID
	cameras := mgr.GetCameras()
	if len(cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cameras))
	}
	if cameras[0].ID != monitorID {
		t.Errorf("expected camera ID %s, got %s", monitorID, cameras[0].ID)
	}
	if cameras[0].Name != "Kamera Depan" {
		t.Errorf("expected friendly name 'Kamera Depan', got %s", cameras[0].Name)
	}
	if cameras[0].ClipCount != 3 {
		t.Errorf("expected 3 clips, got %d", cameras[0].ClipCount)
	}

	// Verify GetDates works with monitorID or alias
	dates := mgr.GetDates(monitorID)
	if len(dates) != 2 {
		t.Fatalf("expected 2 dates, got %d", len(dates))
	}
	if dates[0] != "2026-10-20" || dates[1] != "2026-10-18" {
		t.Errorf("unexpected dates: %v", dates)
	}

	// Also verify GetDates with cam1 alias
	datesByAlias := mgr.GetDates("cam1")
	if len(datesByAlias) != 2 {
		t.Fatalf("expected 2 dates by alias, got %d", len(datesByAlias))
	}

	// Verify GetClips
	clips := mgr.GetClips(monitorID, "2026-10-18")
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips, got %d", len(clips))
	}
	expectedRelPath := filepath.ToSlash(filepath.Join(groupKey, monitorID, "2026-10-18T12-50-01.mp4"))
	if clips[0].RelativePath != expectedRelPath {
		t.Errorf("expected relPath %s, got %s", expectedRelPath, clips[0].RelativePath)
	}

	// Verify GetFullPath safely resolves the nested Shinobi path
	fullPath, err := mgr.GetFullPath(clips[0].RelativePath)
	if err != nil {
		t.Fatalf("failed to resolve full path: %v", err)
	}
	if fullPath != filepath.Join(recDir, "2026-10-18T12-50-01.mp4") {
		t.Errorf("unexpected full path: %s", fullPath)
	}
}
