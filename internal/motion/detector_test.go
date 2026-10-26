package motion

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cctv-monitor/internal/config"
)

func createTestJPEG(path string, col color.Color) error {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, col)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, nil)
}

func TestMotionDetection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_motion_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Recordings: config.RecordingsConfig{
			Dir:                 tmpDir,
			TimezoneOffsetHours: 7,
		},
		Motion: config.MotionConfig{
			Enabled:     true,
			Sensitivity: "medium",
		},
	}

	eventMgr := NewEventManager(cfg)
	detector := NewDetector(cfg, eventMgr)
	detector.SetSensitivity("medium")

	frame1 := filepath.Join(tmpDir, "frame1.jpg")
	frame2 := filepath.Join(tmpDir, "frame2.jpg")
	frame3 := filepath.Join(tmpDir, "frame3.jpg")

	// Frame 1: Black
	if err := createTestJPEG(frame1, color.Black); err != nil {
		t.Fatalf("create frame1: %v", err)
	}
	// Frame 2: Still Black (no motion)
	if err := createTestJPEG(frame2, color.Black); err != nil {
		t.Fatalf("create frame2: %v", err)
	}
	// Frame 3: White (strong motion)
	if err := createTestJPEG(frame3, color.White); err != nil {
		t.Fatalf("create frame3: %v", err)
	}

	// First frame: baseline
	detected, _ := detector.ProcessFrame("cam1", frame1)
	if detected {
		t.Errorf("First frame should not trigger motion")
	}

	// Second frame: identical -> no motion
	detected, pct := detector.ProcessFrame("cam1", frame2)
	if detected {
		t.Errorf("Identical frame should not trigger motion (pct: %.1f)", pct)
	}

	// Third frame: white vs black -> motion!
	detected, pct = detector.ProcessFrame("cam1", frame3)
	if !detected {
		t.Errorf("Different frame should trigger motion (pct: %.1f)", pct)
	}

	// Verify event was saved
	now := time.Now().In(time.FixedZone("WIB", 7*3600))
	events := eventMgr.GetEvents("cam1", now.Format("2006-01-02"))
	if len(events) == 0 {
		t.Errorf("Expected at least 1 motion event, got 0")
	}
}
