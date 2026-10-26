package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadAndSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_cfg_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "config.yaml")

	recTrue := true
	origCfg := &Config{
		ConfigPath: cfgPath,
		Server: ServerConfig{
			Port:       9000,
			StreamsDir: "./test_streams",
		},
		Auth: AuthConfig{
			Enabled:  true,
			Password: "securepassword",
		},
		Recordings: RecordingsConfig{
			Enabled:             true,
			Dir:                 "./test_recordings",
			SegmentMinutes:      10,
			RetentionDays:       14,
			TimezoneOffsetHours: 7,
		},
		Cameras: []Camera{
			{
				ID:     "cam_test",
				Name:   "Front Yard",
				URL:    "rtsp://admin:pass@192.168.1.10:554/live",
				Record: &recTrue,
			},
		},
	}

	// 1. Save config
	if err := Save(cfgPath, origCfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("config.yaml was not created")
	}

	// 2. Load config back
	loadedCfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loadedCfg.Server.Port != 9000 {
		t.Errorf("Expected port 9000, got %d", loadedCfg.Server.Port)
	}
	if loadedCfg.Recordings.SegmentMinutes != 10 {
		t.Errorf("Expected SegmentMinutes 10, got %d", loadedCfg.Recordings.SegmentMinutes)
	}
	if loadedCfg.Recordings.RetentionDays != 14 {
		t.Errorf("Expected RetentionDays 14, got %d", loadedCfg.Recordings.RetentionDays)
	}
	if len(loadedCfg.Cameras) != 1 || loadedCfg.Cameras[0].ID != "cam_test" {
		t.Errorf("Unexpected cameras: %+v", loadedCfg.Cameras)
	}
	if !loadedCfg.Cameras[0].IsRecordingEnabled() {
		t.Errorf("Expected camera recording to be enabled")
	}
}
