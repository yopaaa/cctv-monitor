package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port       int    `yaml:"port"`
	StreamsDir string `yaml:"streams_dir"`
}

// AlarmConfig holds settings for controlling the camera's alarm/siren.
// V380 dan kamera China lainnya sering tidak mendukung ONVIF Relay,
// sehingga kita mendukung beberapa metode:
//   - "onvif"    : Menggunakan ONVIF SetRelayOutputState (standar, tapi jarang didukung)
//   - "http_get" : Mengirim HTTP GET ke URL kustom kamera
//   - "http_post": Mengirim HTTP POST ke URL kustom kamera
type AlarmConfig struct {
	Method string `yaml:"method"` // "onvif", "http_get", "http_post" (default: "onvif")
	OnURL  string `yaml:"on_url"`  // URL untuk nyalakan alarm
	OffURL string `yaml:"off_url"` // URL untuk matikan alarm
}

// Camera represents a single CCTV camera configuration.
// Supports two modes:
//   - Direct RTSP: set the URL field directly.
//   - ONVIF Discovery: set OnvifURL, Username, and Password to auto-discover the RTSP URL.
type Camera struct {
	ID        string      `yaml:"id"`
	Name      string      `yaml:"name"`
	ShinobiID string      `yaml:"shinobi_id"` // Optional: ID monitor di Shinobi (misal: ztIRNMjufs)
	URL       string      `yaml:"url"`       // Direct RTSP stream URL
	OnvifURL  string      `yaml:"onvif_url"` // ONVIF device service URL for auto-discovery
	Username  string      `yaml:"username"`  // Camera credentials (used for ONVIF and/or RTSP)
	Password  string      `yaml:"password"`
	Alarm     AlarmConfig `yaml:"alarm"`     // Alarm/siren configuration
}

// AuthConfig holds dashboard authentication settings.
type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Password string `yaml:"password"`
	Email    string `yaml:"email"` // Default email/username
}

// RecordingsConfig holds settings for CCTV video recordings storage.
type RecordingsConfig struct {
	Dir                 string `yaml:"dir"`             // Directory path to recordings (e.g. ./rekaman_cctv or /mnt/nvr_storage)
	TimezoneOffsetHours int    `yaml:"timezone_offset"` // Timezone offset in hours relative to UTC (default: 7 for UTC+7 / WIB)
}

// Config is the top-level application configuration.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Auth       AuthConfig       `yaml:"auth"`
	Recordings RecordingsConfig `yaml:"recordings"`
	Cameras    []Camera         `yaml:"cameras"`
}

// Load reads and validates the configuration from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Port:       8080,
			StreamsDir: "./streams",
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config YAML: %w", err)
	}

	// Default auth settings
	if cfg.Auth.Password != "" {
		cfg.Auth.Enabled = true
	}
	if cfg.Auth.Email == "" {
		cfg.Auth.Email = "admin"
	}

	// Environment variable override for recordings directory
	if envDir := os.Getenv("RECORDINGS_DIR"); envDir != "" {
		cfg.Recordings.Dir = envDir
	}
	if cfg.Recordings.Dir == "" {
		cfg.Recordings.Dir = "./rekaman_cctv"
	}

	// Environment variable override for recordings timezone offset
	if envTZ := os.Getenv("RECORDINGS_TIMEZONE_OFFSET"); envTZ != "" {
		if val, err := strconv.Atoi(envTZ); err == nil {
			cfg.Recordings.TimezoneOffsetHours = val
		}
	}
	if cfg.Recordings.TimezoneOffsetHours == 0 {
		cfg.Recordings.TimezoneOffsetHours = 7 // Default UTC+7 (WIB)
	}

	// Validate
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return nil, fmt.Errorf("invalid server port: %d", cfg.Server.Port)
	}

	if len(cfg.Cameras) == 0 {
		return nil, fmt.Errorf("no cameras defined in config")
	}

	seen := make(map[string]bool)
	for i, cam := range cfg.Cameras {
		if cam.ID == "" {
			return nil, fmt.Errorf("camera[%d]: 'id' is required", i)
		}
		if seen[cam.ID] {
			return nil, fmt.Errorf("camera[%d]: duplicate id %q", i, cam.ID)
		}
		seen[cam.ID] = true

		if cam.Name == "" {
			cfg.Cameras[i].Name = cam.ID
		}

		if cam.URL == "" && cam.OnvifURL == "" {
			return nil, fmt.Errorf("camera %q: either 'url' (RTSP) or 'onvif_url' is required", cam.ID)
		}
	}

	return cfg, nil
}
