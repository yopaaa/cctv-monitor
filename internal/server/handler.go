package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/motion"
	"cctv-monitor/internal/onvif"
	"cctv-monitor/internal/recorder"
	"cctv-monitor/internal/recording"
	"cctv-monitor/internal/stream"
	"cctv-monitor/web"
)

const sessionCookieName = "cctv_session"
const sessionDuration = 30 * 24 * time.Hour // 30 hari

// Server is the HTTP server that serves the web dashboard and HLS streams.
type Server struct {
	cfg            *config.Config
	mgr            *stream.Manager
	recMgr         *recording.Manager
	recEngine      *recorder.Engine
	motionDetector *motion.Detector
	motionEvents   *motion.EventManager
	mux            *http.ServeMux
	sessions       map[string]time.Time
	sessMu         sync.RWMutex
}

// New creates a new HTTP server with all routes configured.
func New(cfg *config.Config, mgr *stream.Manager, recMgr *recording.Manager, recEngine *recorder.Engine, motionDetector *motion.Detector, motionEvents *motion.EventManager) *Server {
	s := &Server{
		cfg:            cfg,
		mgr:            mgr,
		recMgr:         recMgr,
		recEngine:      recEngine,
		motionDetector: motionDetector,
		motionEvents:   motionEvents,
		mux:            http.NewServeMux(),
		sessions:       make(map[string]time.Time),
	}
	s.routes()
	return s
}

// routes registers all HTTP handlers.
func (s *Server) routes() {
	// Public auth endpoints
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)

	// Protected REST APIs
	s.mux.HandleFunc("GET /api/cameras", s.requireAuth(s.handleCameras))
	s.mux.HandleFunc("POST /api/cameras/{id}/alarm", s.requireAuth(s.handleAlarm))

	// Recordings REST APIs (HTTP Range / Chunked Streaming)
	s.mux.HandleFunc("GET /api/recordings/cameras", s.requireAuth(s.handleRecordingCameras))
	s.mux.HandleFunc("GET /api/recordings/dates", s.requireAuth(s.handleRecordingDates))
	s.mux.HandleFunc("GET /api/recordings/timeline", s.requireAuth(s.handleRecordingTimeline))
	s.mux.HandleFunc("GET /api/recordings/motion", s.requireAuth(s.handleRecordingMotion))
	s.mux.HandleFunc("/api/recordings/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Range, Authorization, Cache-Control")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
			w.WriteHeader(http.StatusOK)
			return
		}
		s.requireAuth(s.handleRecordingStream)(w, r)
	})

	// Settings & Management REST APIs
	s.mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	s.mux.HandleFunc("POST /api/settings/cameras", s.requireAuth(s.handleAddCamera))
	s.mux.HandleFunc("PUT /api/settings/cameras/{id}", s.requireAuth(s.handleUpdateCamera))
	s.mux.HandleFunc("DELETE /api/settings/cameras/{id}", s.requireAuth(s.handleDeleteCamera))
	s.mux.HandleFunc("POST /api/settings/cameras/test", s.requireAuth(s.handleTestCamera))
	s.mux.HandleFunc("PUT /api/settings/recording", s.requireAuth(s.handleUpdateRecordingSettings))
	s.mux.HandleFunc("PUT /api/settings/security", s.requireAuth(s.handleUpdateSecuritySettings))
	s.mux.HandleFunc("GET /api/settings/storage", s.requireAuth(s.handleGetStorageStatus))

	// HLS stream segments — served from filesystem (runtime files)
	streamsDir := http.Dir(s.mgr.StreamsDir())
	streamsHandler := http.StripPrefix("/streams/", http.FileServer(streamsDir))
	s.mux.HandleFunc("/streams/", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "private, no-cache, no-store, must-revalidate, no-transform")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else if strings.HasSuffix(r.URL.Path, ".ts") {
			w.Header().Set("Content-Type", "video/mp2t")
		}

		streamsHandler.ServeHTTP(w, r)
	}))

	// Web dashboard — embedded static files (HTML/CSS/JS) and SPA/path fallback
	s.mux.HandleFunc("/", s.handleStaticAndSPA)
}

// handleStaticAndSPA serves embedded static files or falls back to index.html
// for custom email/user paths (e.g. /myemail@domain.com).
func (s *Server) handleStaticAndSPA(w http.ResponseWriter, r *http.Request) {
	// Prevent browser from caching old JS/CSS/HTML
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	// Try serving static file from embedded FS
	f, err := web.StaticFiles.Open(path)
	if err == nil {
		defer f.Close()
		stat, _ := f.Stat()
		if stat != nil && !stat.IsDir() {
			http.FileServer(http.FS(web.StaticFiles)).ServeHTTP(w, r)
			return
		}
	}

	// If path is not an asset file (doesn't contain .css, .js, .png, etc.),
	// serve index.html to allow path-based email (e.g. /user@email.com)
	if !strings.Contains(path, ".") {
		indexData, err := fs.ReadFile(web.StaticFiles, "index.html")
		if err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexData)
			return
		}
	}

	http.NotFound(w, r)
}

// --- Authentication Middleware & Handlers ---

func (s *Server) isAuthenticated(r *http.Request) bool {
	if !s.cfg.Auth.Enabled {
		return true
	}

	// 1. Check Cookie
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if s.validateToken(cookie.Value) {
			return true
		}
	}

	// 2. Check Authorization Header (Bearer token)
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if s.validateToken(token) {
			return true
		}
	}

	// 3. Check Query parameter ?token=...
	if token := r.URL.Query().Get("token"); token != "" {
		if s.validateToken(token) {
			return true
		}
	}

	return false
}

func (s *Server) validateToken(token string) bool {
	s.sessMu.RLock()
	expiry, exists := s.sessions[token]
	s.sessMu.RUnlock()

	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		s.sessMu.Lock()
		delete(s.sessions, token)
		s.sessMu.Unlock()
		return false
	}
	return true
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthenticated(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false,
				"error":   "Unauthorized. Silakan login terlebih dahulu.",
			})
			return
		}
		next(w, r)
	}
}

// handleLogin processes login requests.
// Request: POST /api/login {"password": "...", "email": "..."}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !s.cfg.Auth.Enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Authentication disabled",
		})
		return
	}

	var req struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Request body tidak valid",
		})
		return
	}

	if req.Password != s.cfg.Auth.Password {
		log.Printf("[AUTH] Failed login attempt with email: %s", req.Email)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   "Password salah!",
		})
		return
	}

	// Create session token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	s.sessMu.Lock()
	s.sessions[token] = time.Now().Add(sessionDuration)
	s.sessMu.Unlock()

	// Detect HTTPS (including Cloudflare Tunnel / reverse proxies)
	isHTTPS := r.TLS != nil ||
		r.Header.Get("X-Forwarded-Proto") == "https" ||
		strings.Contains(r.Header.Get("CF-Visitor"), "https")

	// Set HttpOnly cookie
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(sessionDuration),
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS,
	})

	log.Printf("[AUTH] Login success for user: %s (HTTPS: %v)", req.Email, isHTTPS)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   token,
		"email":   req.Email,
	})
}

// handleLogout ends the user session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		s.sessMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessMu.Unlock()
	}

	isHTTPS := r.TLS != nil ||
		r.Header.Get("X-Forwarded-Proto") == "https" ||
		strings.Contains(r.Header.Get("CF-Visitor"), "https")

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleAuthStatus returns current authentication state.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	authenticated := s.isAuthenticated(r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_required": s.cfg.Auth.Enabled,
		"authenticated": authenticated,
		"default_email": s.cfg.Auth.Email,
	})
}

// --- Camera & Stream Handlers ---

// handleCameras returns the list of cameras with their current status as JSON.
func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")

	cameras := s.mgr.GetCameras()
	if err := json.NewEncoder(w).Encode(cameras); err != nil {
		log.Printf("[ERROR] Failed to encode cameras JSON: %v", err)
	}
}

// handleAlarm triggers or stops the alarm/siren on a camera.
func (s *Server) handleAlarm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	camID := r.PathValue("id")

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	if req.Action != "on" && req.Action != "off" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "action must be 'on' or 'off'",
		})
		return
	}

	cam, ok := s.mgr.GetCamera(camID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("camera %q not found", camID),
		})
		return
	}

	active := req.Action == "on"
	method := cam.Alarm.Method
	if method == "" {
		method = "onvif"
	}

	var err error

	switch method {
	case "onvif":
		if cam.OnvifURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "ONVIF URL belum dikonfigurasi. Tambahkan 'onvif_url' di config.yaml",
			})
			return
		}
		log.Printf("[%s] Alarm %s via ONVIF relay", camID, req.Action)
		err = onvif.TriggerAlarm(cam.OnvifURL, cam.Username, cam.Password, active)

	case "http_get", "http_post":
		targetURL := cam.Alarm.OffURL
		if active {
			targetURL = cam.Alarm.OnURL
		}
		if targetURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("alarm '%s_url' belum dikonfigurasi di config.yaml", req.Action),
			})
			return
		}
		log.Printf("[%s] Alarm %s via %s → %s", camID, req.Action, method, targetURL)
		err = triggerHTTPAlarm(method, targetURL, cam.Username, cam.Password)

	default:
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("metode alarm tidak dikenal: %q", method),
		})
		return
	}

	if err != nil {
		log.Printf("[%s] Alarm %s failed: %v", camID, req.Action, err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("alarm gagal: %v", err),
		})
		return
	}

	log.Printf("[%s] Alarm %s — OK", camID, req.Action)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"camera":  camID,
		"alarm":   req.Action,
	})
}

// --- Recordings Handlers (Playback & Chunked HTTP Range Streaming) ---

// handleRecordingCameras returns the list of cameras/monitors found in the recordings directory.
func (s *Server) handleRecordingCameras(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	cameras := s.recMgr.GetCameras()
	writeJSON(w, http.StatusOK, cameras)
}

// handleRecordingDates returns the dates (YYYY-MM-DD) with available recordings for a camera.
func (s *Server) handleRecordingDates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	camID := r.URL.Query().Get("camera")
	dates := s.recMgr.GetDates(camID)
	writeJSON(w, http.StatusOK, dates)
}

// handleRecordingTimeline returns the video clips and motion events for a camera and date.
func (s *Server) handleRecordingTimeline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	camID := r.URL.Query().Get("camera")
	date := r.URL.Query().Get("date")

	clips := s.recMgr.GetClips(camID, date)

	var events []motion.Event
	if s.motionEvents != nil && camID != "" && date != "" {
		events = s.motionEvents.GetEvents(camID, date)
	}
	if events == nil {
		events = []motion.Event{}
	}

	response := map[string]interface{}{
		"clips":         clips,
		"motion_events": events,
	}
	writeJSON(w, http.StatusOK, response)
}

// handleRecordingMotion returns detected motion intervals for a camera and date.
func (s *Server) handleRecordingMotion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	camID := r.URL.Query().Get("camera")
	date := r.URL.Query().Get("date")

	var events []motion.Event
	if s.motionEvents != nil && camID != "" && date != "" {
		events = s.motionEvents.GetEvents(camID, date)
	}
	if events == nil {
		events = []motion.Event{}
	}

	writeJSON(w, http.StatusOK, events)
}

// handleRecordingStream streams a recorded MP4 video using HTTP Range Requests (Status 206 Partial Content).
// Caps response chunk sizes (512KB for initial chunk, 1MB for subsequent) so slow tunnels/Cloudflare
// don't get choked by full 20MB downloads, providing fast instant playback like YouTube.
func (s *Server) handleRecordingStream(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("file")
	if relPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "parameter 'file' diperlukan",
		})
		return
	}

	fullPath, err := s.recMgr.GetFullPath(relPath)
	if err != nil {
		log.Printf("[RECORDING] Access error for %q: %v", relPath, err)
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "File rekaman tidak ditemukan atau tidak valid",
		})
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		log.Printf("[RECORDING] Open error for %q: %v", fullPath, err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "Gagal membuka file rekaman",
		})
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "Gagal membaca info file rekaman",
		})
		return
	}

	fileSize := stat.Size()
	modTime := stat.ModTime()

	// Anti-buffer headers for Cloudflare and proxies
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Range, Authorization, Cache-Control")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, no-cache, no-store, must-revalidate, no-transform")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "video/mp4")

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// If no Range header, use http.ServeContent which handles full file / caching
		http.ServeContent(w, r, stat.Name(), modTime, file)
		return
	}

	start, end, err := parseByteRange(rangeHeader, fileSize)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		http.Error(w, "Requested Range Not Satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	// Dynamic chunk capping:
	// Initial chunk (start == 0): 512KB for instant playback startup
	// Subsequent chunks: 1MB for smooth playback buffering
	maxChunk := int64(1024 * 1024) // 1MB
	if start == 0 {
		maxChunk = int64(512 * 1024) // 512KB
	}

	if end-start+1 > maxChunk {
		end = start + maxChunk - 1
	}
	if end >= fileSize {
		end = fileSize - 1
	}

	chunkLength := end - start + 1

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	w.Header().Set("Content-Length", strconv.FormatInt(chunkLength, 10))
	w.WriteHeader(http.StatusPartialContent)

	if _, err := file.Seek(start, io.SeekStart); err != nil {
		log.Printf("[RECORDING] Seek error: %v", err)
		return
	}

	log.Printf("[RECORDING] Stream %s bytes %d-%d/%d (%d KB)", stat.Name(), start, end, fileSize, chunkLength/1024)
	_, _ = io.CopyN(w, file, chunkLength)
}

func parseByteRange(rangeHeader string, size int64) (start, end int64, err error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, 0, fmt.Errorf("invalid range unit")
	}
	raw := strings.TrimPrefix(rangeHeader, "bytes=")
	if idx := strings.Index(raw, ","); idx != -1 {
		raw = raw[:idx]
	}
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, fmt.Errorf("invalid suffix range")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, nil
	}

	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, fmt.Errorf("invalid start range")
	}

	if parts[1] == "" {
		return start, size - 1, nil
	}

	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, fmt.Errorf("invalid end range")
	}
	if end >= size {
		end = size - 1
	}

	return start, end, nil
}

func triggerHTTPAlarm(method, targetURL, username, password string) error {
	httpMethod := "GET"
	if method == "http_post" {
		httpMethod = "POST"
	}

	req, err := http.NewRequest(httpMethod, targetURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if username != "" {
		req.SetBasicAuth(username, password)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	return nil
}

// handleGetSettings returns complete configuration and storage status.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	disk, _ := recorder.GetDiskUsage(s.cfg.Recordings.Dir)

	resp := map[string]interface{}{
		"cameras":   s.cfg.Cameras,
		"recording": s.cfg.Recordings,
		"motion":    s.cfg.Motion,
		"storage":   disk,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAddCamera creates a new camera and starts its live stream and recorder.
func (s *Server) handleAddCamera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var cam config.Camera
	if err := json.NewDecoder(r.Body).Decode(&cam); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Format data kamera tidak valid: " + err.Error(),
		})
		return
	}

	cam.Name = strings.TrimSpace(cam.Name)
	if cam.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Nama kamera wajib diisi",
		})
		return
	}

	cam.URL = strings.TrimSpace(cam.URL)
	cam.OnvifURL = strings.TrimSpace(cam.OnvifURL)
	if cam.URL == "" && cam.OnvifURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "URL RTSP atau ONVIF wajib diisi",
		})
		return
	}

	cam.ID = strings.TrimSpace(cam.ID)
	if cam.ID == "" {
		cam.ID = fmt.Sprintf("cam_%d", time.Now().Unix())
	}
	// Sanitize ID: lowercase alphanumeric + underscore/dash
	cam.ID = strings.ToLower(cam.ID)
	cam.ID = strings.ReplaceAll(cam.ID, " ", "_")

	if cam.Record == nil {
		recTrue := true
		cam.Record = &recTrue
	}

	// Add to stream manager
	if err := s.mgr.AddCamera(r.Context(), cam); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Add to recorder engine
	if s.recEngine != nil {
		s.recEngine.AddCamera(r.Context(), cam)
	}

	// Save configuration to file
	if s.cfg.ConfigPath != "" {
		if err := config.Save(s.cfg.ConfigPath, s.cfg); err != nil {
			log.Printf("[SETTINGS] Warning: failed to save config.yaml: %v", err)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"camera":  cam,
	})
}

// handleUpdateCamera updates an existing camera's config and restarts its stream/recorder.
func (s *Server) handleUpdateCamera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	camID := r.PathValue("id")
	if camID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "ID kamera wajib diisi",
		})
		return
	}

	var cam config.Camera
	if err := json.NewDecoder(r.Body).Decode(&cam); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Format data kamera tidak valid: " + err.Error(),
		})
		return
	}

	cam.ID = camID
	cam.Name = strings.TrimSpace(cam.Name)
	if cam.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Nama kamera wajib diisi",
		})
		return
	}

	cam.URL = strings.TrimSpace(cam.URL)
	cam.OnvifURL = strings.TrimSpace(cam.OnvifURL)
	if cam.URL == "" && cam.OnvifURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "URL RTSP atau ONVIF wajib diisi",
		})
		return
	}

	if cam.Record == nil {
		recTrue := true
		cam.Record = &recTrue
	}

	if err := s.mgr.UpdateCamera(r.Context(), cam); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if s.recEngine != nil {
		s.recEngine.UpdateCamera(r.Context(), cam)
	}

	if s.cfg.ConfigPath != "" {
		if err := config.Save(s.cfg.ConfigPath, s.cfg); err != nil {
			log.Printf("[SETTINGS] Warning: failed to save config.yaml: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"camera":  cam,
	})
}

// handleDeleteCamera stops and removes a camera from both manager and config.
func (s *Server) handleDeleteCamera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	camID := r.PathValue("id")
	if camID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "ID kamera wajib diisi",
		})
		return
	}

	if err := s.mgr.RemoveCamera(camID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if s.recEngine != nil {
		s.recEngine.RemoveCamera(camID)
	}

	if s.cfg.ConfigPath != "" {
		if err := config.Save(s.cfg.ConfigPath, s.cfg); err != nil {
			log.Printf("[SETTINGS] Warning: failed to save config.yaml: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Kamera berhasil dihapus",
	})
}

// handleTestCamera tests connectivity to a camera's RTSP stream using FFmpeg probe.
func (s *Server) handleTestCamera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var req struct {
		URL      string `json:"url"`
		OnvifURL string `json:"onvif_url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Format request tidak valid: " + err.Error(),
		})
		return
	}

	targetURL := strings.TrimSpace(req.URL)
	if targetURL == "" && req.OnvifURL != "" {
		discovered, err := onvif.GetStreamURI(req.OnvifURL, req.Username, req.Password)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("ONVIF Discovery gagal: %v", err),
			})
			return
		}
		targetURL = discovered
	}

	if targetURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "URL RTSP atau ONVIF tidak boleh kosong",
		})
		return
	}

	// Probe RTSP with FFmpeg (timeout 5s)
	probeCtx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-timeout", "4000000",
		"-i", targetURL,
		"-t", "1",
		"-f", "null",
		"-",
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Gagal terhubung ke RTSP stream: %s", errMsg),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Stream RTSP berhasil terhubung dan dapat dibaca!",
	})
}

// handleUpdateRecordingSettings updates NVR recording preferences and motion detection settings.
func (s *Server) handleUpdateRecordingSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var req struct {
		Enabled           bool    `json:"enabled"`
		SegmentMinutes    int     `json:"segment_minutes"`
		RetentionDays     int     `json:"retention_days"`
		MotionEnabled     *bool   `json:"motion_enabled"`
		MotionSensitivity string  `json:"motion_sensitivity"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Format request tidak valid: " + err.Error(),
		})
		return
	}

	if req.SegmentMinutes <= 0 {
		req.SegmentMinutes = 5
	}
	if req.RetentionDays <= 0 {
		req.RetentionDays = 7
	}

	s.cfg.Recordings.Enabled = req.Enabled
	s.cfg.Recordings.SegmentMinutes = req.SegmentMinutes
	s.cfg.Recordings.RetentionDays = req.RetentionDays

	if req.MotionEnabled != nil {
		s.cfg.Motion.Enabled = *req.MotionEnabled
	}
	if req.MotionSensitivity != "" {
		s.cfg.Motion.Sensitivity = req.MotionSensitivity
		if s.motionDetector != nil {
			s.motionDetector.SetSensitivity(req.MotionSensitivity)
		}
	}

	if s.cfg.ConfigPath != "" {
		if err := config.Save(s.cfg.ConfigPath, s.cfg); err != nil {
			log.Printf("[SETTINGS] Warning: failed to save config: %v", err)
		}
	}

	if s.recEngine != nil {
		s.recEngine.RestartAll(r.Context())
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"recording": s.cfg.Recordings,
		"motion":    s.cfg.Motion,
		"message":   "Pengaturan rekaman & deteksi gerakan berhasil disimpan",
	})
}

// handleUpdateSecuritySettings updates the dashboard login password.
func (s *Server) handleUpdateSecuritySettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Format request tidak valid: " + err.Error(),
		})
		return
	}

	// If current password is set, verify
	if s.cfg.Auth.Enabled && s.cfg.Auth.Password != "" {
		if req.CurrentPassword != s.cfg.Auth.Password {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "Kata sandi saat ini tidak cocok",
			})
			return
		}
	}

	if len(req.NewPassword) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Kata sandi baru minimal 4 karakter",
		})
		return
	}

	s.cfg.Auth.Password = req.NewPassword
	s.cfg.Auth.Enabled = true

	if s.cfg.ConfigPath != "" {
		if err := config.Save(s.cfg.ConfigPath, s.cfg); err != nil {
			log.Printf("[SETTINGS] Warning: failed to save config: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Kata sandi berhasil diperbarui",
	})
}

// handleGetStorageStatus returns current disk capacity.
func (s *Server) handleGetStorageStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	disk, err := recorder.GetDiskUsage(s.cfg.Recordings.Dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, disk)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	return http.ListenAndServe(addr, s.mux)
}
