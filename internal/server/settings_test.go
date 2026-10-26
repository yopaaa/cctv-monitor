package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/motion"
	"cctv-monitor/internal/recorder"
	"cctv-monitor/internal/recording"
	"cctv-monitor/internal/stream"
)

func setupTestServer(t *testing.T) (*Server, string, func()) {
	tmpDir, err := os.MkdirTemp("", "test_srv_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	cfgFile := filepath.Join(tmpDir, "config.yaml")
	recDir := filepath.Join(tmpDir, "recordings")
	streamDir := filepath.Join(tmpDir, "streams")
	_ = os.MkdirAll(recDir, 0755)
	_ = os.MkdirAll(streamDir, 0755)

	recordVal := true
	cfg := &config.Config{
		ConfigPath: cfgFile,
		Server: config.ServerConfig{
			Port:       8080,
			StreamsDir: streamDir,
		},
		Auth: config.AuthConfig{
			Enabled:  true,
			Password: "testpassword",
		},
		Recordings: config.RecordingsConfig{
			Enabled:        true,
			Dir:            recDir,
			SegmentMinutes: 5,
			RetentionDays:  7,
		},
		Cameras: []config.Camera{
			{
				ID:     "cam_initial",
				Name:   "Initial Camera",
				URL:    "rtsp://127.0.0.1:8554/live",
				Record: &recordVal,
			},
		},
	}
	if err := config.Save(cfgFile, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	mgr := stream.NewManager(cfg)
	recMgr := recording.NewManager(cfg)
	recEngine := recorder.New(cfg)
	motionEvents := motion.NewEventManager(cfg)
	motionDetector := motion.NewDetector(cfg, motionEvents)

	srv := New(cfg, mgr, recMgr, recEngine, motionDetector, motionEvents)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return srv, tmpDir, cleanup
}

func authenticateRequest(srv *Server, req *http.Request) {
	srv.sessMu.Lock()
	defer srv.sessMu.Unlock()
	token := "valid_test_token"
	srv.sessions[token] = time.Now().Add(24 * time.Hour) // valid
	req.Header.Set("Authorization", "Bearer "+token)
}

func TestSettingsAPI(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. GET /api/settings
	req := httptest.NewRequest("GET", "/api/settings", nil)
	authenticateRequest(srv, req)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var settingsResp struct {
		Cameras   []config.Camera         `json:"cameras"`
		Recording config.RecordingsConfig `json:"recording"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settingsResp); err != nil {
		t.Fatalf("Unmarshal settings error: %v", err)
	}
	if len(settingsResp.Cameras) != 1 || settingsResp.Cameras[0].ID != "cam_initial" {
		t.Errorf("Unexpected cameras: %+v", settingsResp.Cameras)
	}

	// Verify JSON keys are lowercase for frontend compatibility
	var rawMap map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rawMap); err != nil {
		t.Fatalf("Unmarshal raw map: %v", err)
	}
	recMap, ok := rawMap["recording"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'recording' in JSON: %+v", rawMap)
	}
	if recMap["enabled"] != true {
		t.Errorf("Expected raw JSON key 'enabled' to be true, got %v", recMap["enabled"])
	}
	if recMap["segment_minutes"] != float64(5) {
		t.Errorf("Expected raw JSON key 'segment_minutes' to be 5, got %v", recMap["segment_minutes"])
	}
	if recMap["retention_days"] != float64(7) {
		t.Errorf("Expected raw JSON key 'retention_days' to be 7, got %v", recMap["retention_days"])
	}

	// 2. POST /api/settings/cameras
	newCamPayload := `{"id": "cam_new", "name": "New Camera", "url": "rtsp://127.0.0.1:8554/cam2"}`
	req = httptest.NewRequest("POST", "/api/settings/cameras", bytes.NewBufferString(newCamPayload))
	authenticateRequest(srv, req)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/settings/cameras expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify camera was added to config
	if len(srv.cfg.Cameras) != 2 {
		t.Fatalf("Expected 2 cameras in config, got %d", len(srv.cfg.Cameras))
	}

	// 3. PUT /api/settings/cameras/cam_new
	updateCamPayload := `{"name": "Updated Camera", "url": "rtsp://127.0.0.1:8554/cam2_updated"}`
	req = httptest.NewRequest("PUT", "/api/settings/cameras/cam_new", bytes.NewBufferString(updateCamPayload))
	authenticateRequest(srv, req)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings/cameras expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify updated
	cam, ok := srv.mgr.GetCamera("cam_new")
	if !ok || cam.Name != "Updated Camera" {
		t.Errorf("Camera not updated: %+v", cam)
	}

	// 4. PUT /api/settings/recording
	recPayload := `{"enabled": true, "segment_minutes": 10, "retention_days": 14}`
	req = httptest.NewRequest("PUT", "/api/settings/recording", bytes.NewBufferString(recPayload))
	authenticateRequest(srv, req)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings/recording expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if srv.cfg.Recordings.SegmentMinutes != 10 || srv.cfg.Recordings.RetentionDays != 14 {
		t.Errorf("Recording settings not updated: %+v", srv.cfg.Recordings)
	}

	// 5. PUT /api/settings/security
	secPayload := `{"current_password": "testpassword", "new_password": "newsuperpassword"}`
	req = httptest.NewRequest("PUT", "/api/settings/security", bytes.NewBufferString(secPayload))
	authenticateRequest(srv, req)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings/security expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if srv.cfg.Auth.Password != "newsuperpassword" {
		t.Errorf("Password was not updated: %s", srv.cfg.Auth.Password)
	}

	// 6. DELETE /api/settings/cameras/cam_new
	req = httptest.NewRequest("DELETE", "/api/settings/cameras/cam_new", nil)
	authenticateRequest(srv, req)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/settings/cameras expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(srv.cfg.Cameras) != 1 {
		t.Errorf("Expected 1 camera after delete, got %d", len(srv.cfg.Cameras))
	}
}
