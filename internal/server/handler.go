package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cctv-monitor/internal/config"
	"cctv-monitor/internal/onvif"
	"cctv-monitor/internal/recording"
	"cctv-monitor/internal/stream"
	"cctv-monitor/web"
)

const sessionCookieName = "cctv_session"
const sessionDuration = 30 * 24 * time.Hour // 30 hari

// Server is the HTTP server that serves the web dashboard and HLS streams.
type Server struct {
	cfg      *config.Config
	mgr      *stream.Manager
	recMgr   *recording.Manager
	mux      *http.ServeMux
	sessions map[string]time.Time
	sessMu   sync.RWMutex
}

// New creates a new HTTP server with all routes configured.
func New(cfg *config.Config, mgr *stream.Manager, recMgr *recording.Manager) *Server {
	s := &Server{
		cfg:      cfg,
		mgr:      mgr,
		recMgr:   recMgr,
		mux:      http.NewServeMux(),
		sessions: make(map[string]time.Time),
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
	s.mux.HandleFunc("GET /api/recordings/stream", s.requireAuth(s.handleRecordingStream))

	// HLS stream segments — served from filesystem (runtime files)
	streamsDir := http.Dir(s.mgr.StreamsDir())
	streamsHandler := http.StripPrefix("/streams/", http.FileServer(streamsDir))
	s.mux.HandleFunc("/streams/", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
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

// handleRecordingTimeline returns the video clips for a camera and date, sorted chronologically.
func (s *Server) handleRecordingTimeline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	camID := r.URL.Query().Get("camera")
	date := r.URL.Query().Get("date")

	clips := s.recMgr.GetClips(camID, date)
	writeJSON(w, http.StatusOK, clips)
}

// handleRecordingStream streams a recorded MP4 video using HTTP Range Requests (Status 206 Partial Content).
// This transmits chunks on demand as the user watches or seeks, avoiding network lag and huge buffering.
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

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "video/mp4")

	log.Printf("[RECORDING] Streaming %s (Range: %s)", relPath, r.Header.Get("Range"))

	// http.ServeFile implements RFC 7233 range requests natively
	http.ServeFile(w, r, fullPath)
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	return http.ListenAndServe(addr, s.mux)
}
