/**
 * CCTV Monitor — Dashboard App (Thumbnail + Modal + Alarm + Auth)
 *
 * Alur kerja:
 * 1. Cek autentikasi via GET /api/auth/status
 * 2. Jika auth aktif & belum login:
 *    - Ambil email/username otomatis dari URL path (/user@email.com) atau query (?email=...)
 *    - Tampilkan form login (hanya input Password!)
 * 3. Setelah login:
 *    - Simpan token di localStorage & kirimkan via Authorization header / Cookie
 *    - Tampilkan dashboard grid thumbnail kamera
 *    - Klik kamera → buka modal live stream HLS
 *    - Tombol Alarm di modal
 *    - Tombol Logout di toolbar
 */

(function () {
    "use strict";

    var POLL_INTERVAL = 5000;
    var THUMB_INTERVAL = 5000;
    var RETRY_DELAY = 3000;

    // DOM Elements — Dashboard & Navigation
    var navLiveBtn = document.getElementById("nav-live-btn");
    var navRecordingsBtn = document.getElementById("nav-recordings-btn");
    var navSettingsBtn = document.getElementById("nav-settings-btn");
    var liveView = document.getElementById("live-view");
    var recordingsView = document.getElementById("recordings-view");
    var settingsView = document.getElementById("settings-view");
    var gridSelectorGroup = document.getElementById("grid-selector-group");

    var grid = document.getElementById("camera-grid");
    var gridSelect = document.getElementById("grid-select");
    var statusIndicator = document.getElementById("status-indicator");
    var userProfile = document.getElementById("user-profile");
    var userEmailBadge = document.getElementById("user-email-badge");
    var logoutBtn = document.getElementById("logout-btn");

    // DOM Elements — Recordings / Playback
    var recCameraSelect = document.getElementById("rec-camera-select");
    var calPrevBtn = document.getElementById("cal-prev-btn");
    var calNextBtn = document.getElementById("cal-next-btn");
    var calMonthYear = document.getElementById("cal-month-year");
    var calendarDays = document.getElementById("calendar-days");
    var recSelectedDateLabel = document.getElementById("rec-selected-date-label");
    var recTotalClipsLabel = document.getElementById("rec-total-clips-label");

    var recNowPlayingTitle = document.getElementById("rec-now-playing-title");
    var recNowPlayingMeta = document.getElementById("rec-now-playing-meta");
    var recAutoplayCheck = document.getElementById("rec-autoplay-check");
    var recDownloadBtn = document.getElementById("rec-download-btn");
    var recVideoPlayer = document.getElementById("rec-video-player");
    var recVideoEmpty = document.getElementById("rec-video-empty");
    var recVideoLoading = document.getElementById("rec-video-loading");
    var recVideoError = document.getElementById("rec-video-error");

    var timelineDateTag = document.getElementById("timeline-date-tag");
    var recTimeJump = document.getElementById("rec-time-jump");
    var recTimeJumpBtn = document.getElementById("rec-time-jump-btn");
    var timelineScrubber = document.getElementById("timeline-scrubber");
    var scrubberSegments = document.getElementById("scrubber-segments");
    var scrubberMotionSegments = document.getElementById("scrubber-motion-segments");
    var scrubberPlayhead = document.getElementById("scrubber-playhead");
    var scrubberTooltip = document.getElementById("scrubber-tooltip");
    var hourFilterPills = document.getElementById("hour-filter-pills");
    var recTimelineGrouped = document.getElementById("rec-timeline-grouped");

    // DOM Elements — Modal
    var modalOverlay = document.getElementById("modal-overlay");
    var modalVideo = document.getElementById("modal-video");
    var modalCamName = document.getElementById("modal-cam-name");
    var modalCamStatus = document.getElementById("modal-cam-status");
    var modalCloseBtn = document.getElementById("modal-close");
    var modalSnapshotBtn = document.getElementById("modal-snapshot");
    var modalFullscreenBtn = document.getElementById("modal-fullscreen");
    var modalAlarmBtn = document.getElementById("modal-alarm");

    // DOM Elements — Login Form
    var loginOverlay = document.getElementById("login-overlay");
    var loginForm = document.getElementById("login-form");
    var loginPasswordInput = document.getElementById("login-password");
    var loginEmailDisplay = document.getElementById("login-email-display");
    var loginError = document.getElementById("login-error");
    var loginSubmitBtn = document.getElementById("login-submit-btn");

    // DOM Elements — Settings Subtabs
    var setTabCamerasBtn = document.getElementById("set-tab-cameras-btn");
    var setTabStorageBtn = document.getElementById("set-tab-storage-btn");
    var setTabSecurityBtn = document.getElementById("set-tab-security-btn");
    var setPanelCameras = document.getElementById("set-panel-cameras");
    var setPanelStorage = document.getElementById("set-panel-storage");
    var setPanelSecurity = document.getElementById("set-panel-security");

    // DOM Elements — Settings Camera Panel
    var btnAddCamera = document.getElementById("btn-add-camera");
    var cameraSettingsList = document.getElementById("camera-settings-list");

    // DOM Elements — Settings Storage & NVR Panel
    var btnRefreshStorage = document.getElementById("btn-refresh-storage");
    var storageProgressFill = document.getElementById("storage-progress-fill");
    var storageUsedLabel = document.getElementById("storage-used-label");
    var storagePercentLabel = document.getElementById("storage-percent-label");
    var storageFreeLabel = document.getElementById("storage-free-label");
    var storageTotalVal = document.getElementById("storage-total-val");
    var storageUsedVal = document.getElementById("storage-used-val");
    var storageFreeVal = document.getElementById("storage-free-val");
    var storagePathVal = document.getElementById("storage-path-val");

    var formNvrSettings = document.getElementById("form-nvr-settings");
    var nvrEnableCheck = document.getElementById("nvr-enable-check");
    var nvrSegmentSelect = document.getElementById("nvr-segment-select");
    var nvrRetentionSelect = document.getElementById("nvr-retention-select");
    var nvrMotionCheck = document.getElementById("nvr-motion-check");
    var nvrMotionSensitivitySelect = document.getElementById("nvr-motion-sensitivity-select");
    var btnSaveNvr = document.getElementById("btn-save-nvr");
    var nvrSaveMsg = document.getElementById("nvr-save-msg");

    // DOM Elements — Settings Security Panel
    var formSecuritySettings = document.getElementById("form-security-settings");
    var secCurrentPwd = document.getElementById("sec-current-pwd");
    var secNewPwd = document.getElementById("sec-new-pwd");
    var secConfirmPwd = document.getElementById("sec-confirm-pwd");
    var btnSaveSecurity = document.getElementById("btn-save-security");
    var secSaveMsg = document.getElementById("sec-save-msg");

    // DOM Elements — Camera Modal Form
    var cameraModalOverlay = document.getElementById("camera-modal-overlay");
    var cameraModalTitle = document.getElementById("camera-modal-title");
    var cameraModalClose = document.getElementById("camera-modal-close");
    var cameraForm = document.getElementById("camera-form");
    var camFormMode = document.getElementById("cam-form-mode");
    var camInputName = document.getElementById("cam-input-name");
    var camInputId = document.getElementById("cam-input-id");
    var camInputUrl = document.getElementById("cam-input-url");
    var camInputOnvif = document.getElementById("cam-input-onvif");
    var camInputUser = document.getElementById("cam-input-user");
    var camInputPass = document.getElementById("cam-input-pass");
    var camInputAlarmMethod = document.getElementById("cam-input-alarm-method");
    var camInputAlarmOn = document.getElementById("cam-input-alarm-on");
    var camInputRecord = document.getElementById("cam-input-record");
    var btnTestRtsp = document.getElementById("btn-test-rtsp");
    var testRtspResult = document.getElementById("test-rtsp-result");
    var camFormError = document.getElementById("cam-form-error");
    var btnCancelCam = document.getElementById("btn-cancel-cam");
    var btnSubmitCam = document.getElementById("btn-submit-cam");

    // State — Live
    var currentCameras = [];
    var activeModalCam = null;
    var activeHls = null;
    var thumbTimers = {};
    var pollTimer = null;
    var alarmActive = false;
    var alarmLoading = false;
    var currentEmail = "admin";
    var authToken = localStorage.getItem("cctv_token") || "";

    // State — Recordings
    var currentView = "live";
    var recCameras = [];
    var recSelectedCamera = "";
    var recAvailableDates = [];
    var recSelectedDate = "";
    var calCurrentDate = new Date();
    var calYear = calCurrentDate.getFullYear();
    var calMonth = calCurrentDate.getMonth();
    var recClips = [];
    var recMotionEvents = [];
    var recCurrentClipIndex = -1;
    var selectedHourFilter = "all";

    // ===================================
    // Helper: Authenticated Fetch Wrapper
    // ===================================

    function authFetch(url, options) {
        options = options || {};
        options.headers = options.headers || {};
        options.credentials = "same-origin";

        if (authToken) {
            options.headers["Authorization"] = "Bearer " + authToken;
        }

        return fetch(url, options).then(function (resp) {
            if (resp.status === 401) {
                showLoginScreen();
                throw new Error("Unauthorized");
            }
            return resp;
        });
    }

    // ===================================
    // Helper: Extract Email from Path/URL
    // ===================================

    function resolveUserEmail(defaultFallback) {
        // 1. Cek Query Param (?email=... atau ?user=...)
        var params = new URLSearchParams(window.location.search);
        var qEmail = params.get("email") || params.get("user");
        if (qEmail && qEmail.trim() !== "") {
            var val = qEmail.trim();
            localStorage.setItem("cctv_email", val);
            return val;
        }

        // 2. Cek Path URL (contoh: http://domain.com/yopa@gmail.com)
        var path = window.location.pathname.replace(/^\/+|\/+$/g, "");
        if (path && path !== "index.html" && !path.includes("/")) {
            var decoded = decodeURIComponent(path);
            localStorage.setItem("cctv_email", decoded);
            return decoded;
        }

        // 3. Cek LocalStorage
        var saved = localStorage.getItem("cctv_email");
        if (saved && saved.trim() !== "") {
            return saved;
        }

        // 4. Default dari server / config
        return defaultFallback || "admin";
    }

    // ===================================
    // Authentication Flow
    // ===================================

    function checkAuthAndStart() {
        var statusUrl = "/api/auth/status";
        if (authToken) {
            statusUrl += "?token=" + encodeURIComponent(authToken);
        }

        authFetch(statusUrl, { cache: "no-store" })
            .then(function (resp) { return resp.json(); })
            .then(function (data) {
                currentEmail = resolveUserEmail(data.default_email);
                loginEmailDisplay.textContent = currentEmail;
                userEmailBadge.textContent = "👤 " + currentEmail;

                if (data.auth_required && !data.authenticated) {
                    showLoginScreen();
                } else {
                    hideLoginScreen();
                    startDashboard();
                }
            })
            .catch(function (err) {
                if (err.message !== "Unauthorized") {
                    console.warn("Auth check failed:", err);
                    showLoginScreen();
                }
            });
    }

    function showLoginScreen() {
        loginOverlay.classList.remove("hidden");
        userProfile.classList.add("hidden");
        loginPasswordInput.value = "";
        loginError.classList.add("hidden");
        loginPasswordInput.focus();
        stopAllDashboardLoops();
    }

    function hideLoginScreen() {
        loginOverlay.classList.add("hidden");
        userProfile.classList.remove("hidden");
    }

    // Handle Login Submit
    loginForm.addEventListener("submit", function (e) {
        e.preventDefault();

        var password = loginPasswordInput.value;
        if (!password) return;

        loginSubmitBtn.disabled = true;
        loginSubmitBtn.innerHTML = "<span>Memverifikasi...</span> ⏳";
        loginError.classList.add("hidden");

        fetch("/api/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "same-origin",
            body: JSON.stringify({
                password: password,
                email: currentEmail
            })
        })
        .then(function (resp) {
            return resp.json().then(function (data) {
                return { status: resp.status, data: data };
            });
        })
        .then(function (result) {
            loginSubmitBtn.disabled = false;
            loginSubmitBtn.innerHTML = "<span>Masuk</span> ➔";

            if (result.status === 200 && result.data.success) {
                authToken = result.data.token || "";
                if (authToken) {
                    localStorage.setItem("cctv_token", authToken);
                }
                localStorage.setItem("cctv_email", currentEmail);
                userEmailBadge.textContent = "👤 " + currentEmail;

                hideLoginScreen();
                startDashboard();
            } else {
                loginError.textContent = result.data.error || "Password salah!";
                loginError.classList.remove("hidden");
                loginPasswordInput.focus();
                loginPasswordInput.select();
            }
        })
        .catch(function (err) {
            loginSubmitBtn.disabled = false;
            loginSubmitBtn.innerHTML = "<span>Masuk</span> ➔";
            loginError.textContent = "Gagal terhubung ke server: " + err.message;
            loginError.classList.remove("hidden");
        });
    });

    // Handle Logout
    logoutBtn.addEventListener("click", function () {
        if (!confirm("Apakah Anda yakin ingin keluar?")) return;

        authFetch("/api/logout", { method: "POST" })
            .finally(function () {
                authToken = "";
                localStorage.removeItem("cctv_token");
                showLoginScreen();
            });
    });

    // ===================================
    // Grid Layout
    // ===================================

    function applyGridLayout(value) {
        grid.className = "camera-grid";
        if (value === "auto") {
            grid.classList.add("grid-auto");
        } else {
            grid.classList.add("grid-" + value);
        }
    }

    gridSelect.addEventListener("change", function () {
        applyGridLayout(this.value);
    });

    applyGridLayout(gridSelect.value);

    // ===================================
    // Camera API
    // ===================================

    function fetchCameras() {
        return authFetch("/api/cameras", { cache: "no-store" })
            .then(function (resp) {
                if (!resp.ok) throw new Error("HTTP " + resp.status);
                statusIndicator.className = "status-dot online";
                statusIndicator.title = "Server terhubung";
                return resp.json();
            })
            .catch(function (err) {
                if (err.message !== "Unauthorized") {
                    console.error("Failed to fetch cameras:", err);
                    statusIndicator.className = "status-dot offline";
                    statusIndicator.title = "Server tidak terhubung: " + err.message;
                }
                return null;
            });
    }

    // ===================================
    // Thumbnail Card
    // ===================================

    function getAuthenticatedUrl(baseUrl) {
        if (!authToken) return baseUrl;
        var sep = baseUrl.includes("?") ? "&" : "?";
        return baseUrl + sep + "token=" + encodeURIComponent(authToken);
    }

    function createCameraCard(camera) {
        var card = document.createElement("div");
        card.className = "camera-card";
        card.id = "card-" + camera.id;

        var thumb = document.createElement("img");
        thumb.className = "camera-thumbnail";
        thumb.id = "thumb-" + camera.id;
        thumb.alt = camera.name;
        thumb.loading = "lazy";
        thumb.src = getAuthenticatedUrl("/streams/" + camera.id + "/thumbnail.jpg?t=" + Date.now());
        thumb.onerror = function () {
            this.style.display = "none";
            var placeholder = card.querySelector(".camera-placeholder");
            if (placeholder) placeholder.style.display = "flex";
        };
        thumb.onload = function () {
            this.style.display = "block";
            var placeholder = card.querySelector(".camera-placeholder");
            if (placeholder) placeholder.style.display = "none";
        };

        var placeholder = document.createElement("div");
        placeholder.className = "camera-placeholder";
        placeholder.textContent = "📹";

        var overlay = document.createElement("div");
        overlay.className = "camera-overlay";

        var nameEl = document.createElement("span");
        nameEl.className = "camera-name";
        nameEl.textContent = camera.name;

        var statusEl = document.createElement("span");
        statusEl.className = "camera-status";
        statusEl.id = "status-" + camera.id;

        overlay.appendChild(nameEl);
        overlay.appendChild(statusEl);

        var playOverlay = document.createElement("div");
        playOverlay.className = "play-overlay";

        var playBtn = document.createElement("div");
        playBtn.className = "play-btn";

        playOverlay.appendChild(playBtn);

        card.appendChild(placeholder);
        card.appendChild(thumb);
        card.appendChild(overlay);
        card.appendChild(playOverlay);

        card.addEventListener("click", function () {
            openModal(camera);
        });

        startThumbRefresh(camera.id);

        return card;
    }

    function startThumbRefresh(camId) {
        if (thumbTimers[camId]) clearInterval(thumbTimers[camId]);

        // Preload thumbnail off-screen to prevent flickering and avoid disappearing on network lag
        thumbTimers[camId] = setInterval(function () {
            var thumb = document.getElementById("thumb-" + camId);
            if (!thumb) return;

            var newUrl = getAuthenticatedUrl("/streams/" + camId + "/thumbnail.jpg?t=" + Date.now());
            var preloader = new Image();

            preloader.onload = function () {
                thumb.src = newUrl;
                thumb.style.display = "block";
                var card = document.getElementById("card-" + camId);
                if (card) {
                    var placeholder = card.querySelector(".camera-placeholder");
                    if (placeholder) placeholder.style.display = "none";
                }
            };
            preloader.onerror = function () {
                // Keep the current thumbnail visible on transient network hiccups
            };
            preloader.src = newUrl;
        }, THUMB_INTERVAL);
    }

    // ===================================
    // Status Updates
    // ===================================

    function updateCameraStatus(camera) {
        var statusEl = document.getElementById("status-" + camera.id);
        if (!statusEl) return;

        var dotClass = "offline";
        var label = "Offline";

        switch (camera.status) {
            case "online":
                dotClass = "online";
                label = "Live";
                break;
            case "connecting":
                dotClass = "connecting";
                label = "Connecting...";
                break;
            case "error":
                dotClass = "error";
                label = camera.error || "Error";
                break;
            default:
                dotClass = "offline";
                label = "Offline";
        }

        statusEl.innerHTML =
            '<span class="status-dot ' + dotClass + '"></span> ' + label;

        if (activeModalCam && activeModalCam.id === camera.id) {
            modalCamStatus.innerHTML =
                '<span class="status-dot ' + dotClass + '"></span> ' + label;
        }
    }

    // ===================================
    // Modal — Live Stream Viewer
    // ===================================

    function openModal(camera) {
        activeModalCam = camera;
        alarmActive = false;
        updateAlarmButton();

        modalCamName.textContent = camera.name;
        modalCamStatus.innerHTML = '<span class="status-dot connecting"></span> Connecting...';

        modalOverlay.classList.remove("hidden");
        document.body.style.overflow = "hidden";

        var wrapper = document.querySelector(".modal-video-wrapper");
        var existingLoader = wrapper.querySelector(".modal-loading");
        if (!existingLoader) {
            var loader = document.createElement("div");
            loader.className = "modal-loading";
            loader.innerHTML = '<div class="spinner"></div><span>Memulai stream...</span>';
            wrapper.appendChild(loader);
        }

        startModalStream(camera);
    }

    function closeModal() {
        if (alarmActive && activeModalCam) {
            sendAlarmRequest("off");
        }

        stopModalStream();
        modalOverlay.classList.add("hidden");
        document.body.style.overflow = "";
        activeModalCam = null;
        alarmActive = false;
        alarmLoading = false;
        updateAlarmButton();

        var loader = document.querySelector(".modal-loading");
        if (loader) loader.remove();
    }

    function startModalStream(camera) {
        stopModalStream();

        var hlsUrl = getAuthenticatedUrl(camera.hls_url);

        if (!Hls.isSupported()) {
            modalVideo.src = hlsUrl;
            modalVideo.play().catch(function () {});
            removeLoader();
            return;
        }

        var hls = new Hls({
            liveDurationInfinity: true,
            liveBackBufferLength: 0,
            maxBufferLength: 5,
            maxMaxBufferLength: 10,
            liveSyncDurationCount: 1,
            enableWorker: true,
            lowLatencyMode: false,
            xhrSetup: function (xhr, url) {
                if (authToken) {
                    xhr.setRequestHeader("Authorization", "Bearer " + authToken);
                }
            }
        });

        hls.loadSource(hlsUrl);
        hls.attachMedia(modalVideo);

        hls.on(Hls.Events.MANIFEST_PARSED, function () {
            modalVideo.play().catch(function () {});
            removeLoader();
        });

        hls.on(Hls.Events.ERROR, function (event, data) {
            if (data.fatal) {
                console.warn("[Modal] HLS fatal error:", data.type, data.details);
                hls.destroy();
                activeHls = null;

                if (activeModalCam && activeModalCam.id === camera.id) {
                    setTimeout(function () {
                        if (activeModalCam && activeModalCam.id === camera.id) {
                            startModalStream(camera);
                        }
                    }, RETRY_DELAY);
                }
            }
        });

        activeHls = hls;
    }

    function stopModalStream() {
        if (activeHls) {
            activeHls.destroy();
            activeHls = null;
        }
        modalVideo.removeAttribute("src");
        modalVideo.load();
    }

    function removeLoader() {
        var loader = document.querySelector(".modal-loading");
        if (loader) loader.remove();
    }

    function takeSnapshot() {
        if (!activeModalCam || modalVideo.readyState < 2) return;

        var canvas = document.createElement("canvas");
        canvas.width = modalVideo.videoWidth;
        canvas.height = modalVideo.videoHeight;

        var ctx = canvas.getContext("2d");
        ctx.drawImage(modalVideo, 0, 0);

        var link = document.createElement("a");
        var timestamp = new Date().toISOString().replace(/[:.]/g, "-");
        link.download = activeModalCam.name + "_" + timestamp + ".jpg";
        link.href = canvas.toDataURL("image/jpeg", 0.95);
        link.click();
    }

    // ===================================
    // Alarm Control
    // ===================================

    function toggleAlarm() {
        if (!activeModalCam || alarmLoading) return;

        var newState = !alarmActive;
        var action = newState ? "on" : "off";

        sendAlarmRequest(action);
    }

    function sendAlarmRequest(action) {
        if (!activeModalCam) return;

        alarmLoading = true;
        updateAlarmButton();

        authFetch("/api/cameras/" + activeModalCam.id + "/alarm", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: action })
        })
        .then(function (resp) {
            return resp.json();
        })
        .then(function (data) {
            alarmLoading = false;
            if (data.success) {
                alarmActive = (action === "on");
                updateAlarmButton();
            } else {
                updateAlarmButton();
                alert("⚠️ Alarm gagal:\n" + (data.error || "Unknown error"));
            }
        })
        .catch(function (err) {
            alarmLoading = false;
            updateAlarmButton();
            if (err.message !== "Unauthorized") {
                alert("⚠️ Alarm error:\n" + err.message);
            }
        });
    }

    function updateAlarmButton() {
        if (alarmLoading) {
            modalAlarmBtn.textContent = "🔊 Loading...";
            modalAlarmBtn.classList.remove("alarm-active");
            modalAlarmBtn.disabled = true;
        } else if (alarmActive) {
            modalAlarmBtn.textContent = "🔊 ALARM ON";
            modalAlarmBtn.classList.add("alarm-active");
            modalAlarmBtn.disabled = false;
        } else {
            modalAlarmBtn.textContent = "🔊 Alarm";
            modalAlarmBtn.classList.remove("alarm-active");
            modalAlarmBtn.disabled = false;
        }
    }

    // ===================================
    // Modal Event Listeners
    // ===================================

    modalCloseBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        closeModal();
    });

    modalOverlay.addEventListener("click", function (e) {
        if (e.target === modalOverlay) {
            closeModal();
        }
    });

    document.addEventListener("keydown", function (e) {
        if (e.key === "Escape" && activeModalCam) {
            closeModal();
        }
    });

    modalSnapshotBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        takeSnapshot();
    });

    function isMobileDevice() {
        return (
            /Android|iPhone|iPad|iPod|Mobile/i.test(navigator.userAgent) ||
            window.matchMedia("(max-width: 768px)").matches ||
            ("ontouchstart" in window && window.innerWidth <= 1024)
        );
    }

    function toggleFullscreen() {
        var container = document.querySelector(".modal-video-wrapper");
        var video = document.getElementById("modal-video");

        var isFullscreen =
            document.fullscreenElement ||
            document.webkitFullscreenElement ||
            document.mozFullScreenElement ||
            document.msFullscreenElement;

        if (isFullscreen) {
            if (document.exitFullscreen) {
                document.exitFullscreen();
            } else if (document.webkitExitFullscreen) {
                document.webkitExitFullscreen();
            }
            if (screen.orientation && screen.orientation.unlock) {
                try {
                    screen.orientation.unlock();
                } catch (e) {}
            }
        } else {
            var isMobile = isMobileDevice();

            // iOS Safari native video fullscreen
            if (video && video.webkitEnterFullscreen && (!container.requestFullscreen && !container.webkitRequestFullscreen)) {
                video.webkitEnterFullscreen();
                return;
            }

            var reqPromise = null;
            if (container.requestFullscreen) {
                reqPromise = container.requestFullscreen();
            } else if (container.webkitRequestFullscreen) {
                reqPromise = container.webkitRequestFullscreen();
            } else if (video && video.webkitEnterFullscreen) {
                video.webkitEnterFullscreen();
                return;
            }

            // Khusus HP (Mobile): Otomatis kunci ke posisi Landscape (Layar Miring)
            if (isMobile && screen.orientation && screen.orientation.lock) {
                if (reqPromise && reqPromise.then) {
                    reqPromise
                        .then(function () {
                            return screen.orientation.lock("landscape");
                        })
                        .catch(function (err) {
                            console.log("Landscape lock error / not permitted:", err);
                        });
                } else {
                    try {
                        screen.orientation.lock("landscape").catch(function () {});
                    } catch (e) {}
                }
            }
        }
    }

    function handleFullscreenChange() {
        var isFullscreen =
            document.fullscreenElement ||
            document.webkitFullscreenElement;

        if (!isFullscreen) {
            if (screen.orientation && screen.orientation.unlock) {
                try {
                    screen.orientation.unlock();
                } catch (e) {}
            }
        }
    }

    document.addEventListener("fullscreenchange", handleFullscreenChange);
    document.addEventListener("webkitfullscreenchange", handleFullscreenChange);

    modalFullscreenBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        toggleFullscreen();
    });

    modalAlarmBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        toggleAlarm();
    });

    // ===================================
    // View Switcher (Live vs Rekaman vs Pengaturan)
    // ===================================

    function switchView(view) {
        currentView = view;
        if (view === "live") {
            navLiveBtn.classList.add("active");
            if (navRecordingsBtn) navRecordingsBtn.classList.remove("active");
            if (navSettingsBtn) navSettingsBtn.classList.remove("active");
            liveView.classList.remove("hidden");
            if (recordingsView) recordingsView.classList.add("hidden");
            if (settingsView) settingsView.classList.add("hidden");
            if (gridSelectorGroup) gridSelectorGroup.style.display = "";
            if (recVideoPlayer) recVideoPlayer.pause();
        } else if (view === "recordings") {
            navLiveBtn.classList.remove("active");
            if (navRecordingsBtn) navRecordingsBtn.classList.add("active");
            if (navSettingsBtn) navSettingsBtn.classList.remove("active");
            liveView.classList.add("hidden");
            if (recordingsView) recordingsView.classList.remove("hidden");
            if (settingsView) settingsView.classList.add("hidden");
            if (gridSelectorGroup) gridSelectorGroup.style.display = "none";
            if (activeModalCam) closeModal();
            initRecordingsView();
        } else if (view === "settings") {
            navLiveBtn.classList.remove("active");
            if (navRecordingsBtn) navRecordingsBtn.classList.remove("active");
            if (navSettingsBtn) navSettingsBtn.classList.add("active");
            liveView.classList.add("hidden");
            if (recordingsView) recordingsView.classList.add("hidden");
            if (settingsView) settingsView.classList.remove("hidden");
            if (gridSelectorGroup) gridSelectorGroup.style.display = "none";
            if (activeModalCam) closeModal();
            if (recVideoPlayer) recVideoPlayer.pause();
            loadSettingsData();
        }
    }

    if (navLiveBtn) {
        navLiveBtn.addEventListener("click", function () {
            switchView("live");
        });
    }

    if (navRecordingsBtn) {
        navRecordingsBtn.addEventListener("click", function () {
            switchView("recordings");
        });
    }

    if (navSettingsBtn) {
        navSettingsBtn.addEventListener("click", function () {
            switchView("settings");
        });
    }

    // ===================================
    // Recordings Module (Calendar, Timeline & Chunked Player)
    // ===================================

    var MONTH_NAMES = [
        "Januari", "Februari", "Maret", "April", "Mei", "Juni",
        "Juli", "Agustus", "September", "Oktober", "November", "Desember"
    ];

    function initRecordingsView() {
        loadRecordingCameras().then(function () {
            loadRecordingDates().then(function () {
                renderCalendar();
                if (recAvailableDates.length > 0 && !recSelectedDate) {
                    selectRecordingDate(recAvailableDates[0]);
                }
            });
        });
    }

    function loadRecordingCameras() {
        return authFetch("/api/recordings/cameras")
            .then(function (resp) { return resp.json(); })
            .then(function (cameras) {
                recCameras = cameras || [];
                recCameraSelect.innerHTML = "";

                if (recCameras.length === 0) {
                    recCameraSelect.innerHTML = '<option value="">Tidak ada rekaman</option>';
                    return;
                }

                recCameras.forEach(function (c) {
                    var opt = document.createElement("option");
                    opt.value = c.id;
                    opt.textContent = c.name + " (" + c.clip_count + " klip)";
                    recCameraSelect.appendChild(opt);
                });

                if (!recSelectedCamera && recCameras.length > 0) {
                    recSelectedCamera = recCameras[0].id;
                }
                recCameraSelect.value = recSelectedCamera;
            })
            .catch(function (err) {
                console.error("Gagal load recording cameras:", err);
            });
    }

    function loadRecordingDates() {
        var url = "/api/recordings/dates";
        if (recSelectedCamera) {
            url += "?camera=" + encodeURIComponent(recSelectedCamera);
        }

        return authFetch(url)
            .then(function (resp) { return resp.json(); })
            .then(function (dates) {
                recAvailableDates = dates || [];

                // Automatically align calendar to latest recorded date
                if (recAvailableDates.length > 0) {
                    var latestParts = recAvailableDates[0].split("-");
                    if (latestParts.length === 3) {
                        calYear = parseInt(latestParts[0], 10);
                        calMonth = parseInt(latestParts[1], 10) - 1;
                    }
                }
            })
            .catch(function (err) {
                console.error("Gagal load dates:", err);
            });
    }

    function renderCalendar() {
        if (!calMonthYear || !calendarDays) return;

        calMonthYear.textContent = MONTH_NAMES[calMonth] + " " + calYear;
        calendarDays.innerHTML = "";

        var firstDayIndex = new Date(calYear, calMonth, 1).getDay();
        var daysInMonth = new Date(calYear, calMonth + 1, 0).getDate();

        // Empty cells before first day
        for (var i = 0; i < firstDayIndex; i++) {
            var emptyCell = document.createElement("div");
            emptyCell.className = "cal-day empty";
            calendarDays.appendChild(emptyCell);
        }

        // Render days
        for (var day = 1; day <= daysInMonth; day++) {
            var dayStr = (day < 10 ? "0" : "") + day;
            var monthStr = (calMonth + 1 < 10 ? "0" : "") + (calMonth + 1);
            var dateISO = calYear + "-" + monthStr + "-" + dayStr;

            var dayBtn = document.createElement("div");
            dayBtn.className = "cal-day";
            dayBtn.textContent = day;

            var hasRec = recAvailableDates.indexOf(dateISO) !== -1;
            if (hasRec) {
                dayBtn.classList.add("has-recording");
                dayBtn.title = "Ada rekaman video";
            }

            if (dateISO === recSelectedDate) {
                dayBtn.classList.add("selected");
            }

            (function (dISO) {
                dayBtn.addEventListener("click", function () {
                    selectRecordingDate(dISO);
                });
            })(dateISO);

            calendarDays.appendChild(dayBtn);
        }
    }

    function selectRecordingDate(dateISO) {
        recSelectedDate = dateISO;
        selectedHourFilter = "all";

        var parts = dateISO.split("-");
        var displayLabel = parts.length === 3 ? parts[2] + " " + MONTH_NAMES[parseInt(parts[1], 10) - 1] + " " + parts[0] : dateISO;

        recSelectedDateLabel.textContent = displayLabel;
        timelineDateTag.textContent = displayLabel;

        renderCalendar();
        loadTimelineClips(recSelectedCamera, dateISO);
    }

    function loadTimelineClips(camID, dateISO) {
        if (recTimelineGrouped) {
            recTimelineGrouped.innerHTML = '<div class="timeline-empty">Memuat rekaman...</div>';
        }
        recCurrentClipIndex = -1;

        var url = "/api/recordings/timeline?camera=" + encodeURIComponent(camID) + "&date=" + encodeURIComponent(dateISO);
        authFetch(url)
            .then(function (resp) { return resp.json(); })
            .then(function (data) {
                if (Array.isArray(data)) {
                    recClips = data;
                    recMotionEvents = [];
                } else if (data && typeof data === "object") {
                    recClips = data.clips || [];
                    recMotionEvents = data.motion_events || [];
                } else {
                    recClips = [];
                    recMotionEvents = [];
                }
                recTotalClipsLabel.textContent = recClips.length + " klip";
                renderTimelineClips();
            })
            .catch(function (err) {
                if (recTimelineGrouped) {
                    recTimelineGrouped.innerHTML = '<div class="timeline-empty">Gagal memuat: ' + err.message + '</div>';
                }
            });
    }

    function timeStrToSeconds(str) {
        if (!str) return 0;
        var parts = str.split(":");
        var h = parseInt(parts[0], 10) || 0;
        var m = parseInt(parts[1], 10) || 0;
        var s = parseInt(parts[2], 10) || 0;
        return h * 3600 + m * 60 + s;
    }

    function secondsToTimeStr(totalSec) {
        var h = Math.floor(totalSec / 3600) % 24;
        var m = Math.floor((totalSec % 3600) / 60);
        var s = Math.floor(totalSec % 60);
        var hh = (h < 10 ? "0" : "") + h;
        var mm = (m < 10 ? "0" : "") + m;
        var ss = (s < 10 ? "0" : "") + s;
        return hh + ":" + mm + ":" + ss;
    }

    function renderTimelineClips() {
        if (!recTimelineGrouped) return;

        recTimelineGrouped.innerHTML = "";

        if (recClips.length === 0) {
            recTimelineGrouped.innerHTML = '<div class="timeline-empty">Tidak ada video rekaman pada tanggal ini</div>';
            if (scrubberSegments) scrubberSegments.innerHTML = "";
            if (scrubberMotionSegments) scrubberMotionSegments.innerHTML = "";
            if (hourFilterPills) hourFilterPills.innerHTML = "";
            if (scrubberPlayhead) scrubberPlayhead.style.display = "none";
            return;
        }

        // Process clips with seconds and group by hour
        var hourGroups = {};
        for (var i = 0; i < recClips.length; i++) {
            var clip = recClips[i];
            clip.startSec = timeStrToSeconds(clip.time);
            clip.durationSec = 300; // default 5 min
            clip.origIdx = i;

            var hourKey = clip.time.substring(0, 2); // e.g. "13", "14"
            if (!hourGroups[hourKey]) {
                hourGroups[hourKey] = [];
            }
            hourGroups[hourKey].push(clip);
        }

        // 1. Render Interactive 24H Scrubber Bar
        render24HScrubber();

        // 2. Render Hour Quick Filter Pills
        renderHourFilterPills(hourGroups);

        // 3. Render Neatly Grouped Clips List
        renderGroupedClipsList(hourGroups);
    }

    function render24HScrubber() {
        if (!scrubberSegments) return;
        scrubberSegments.innerHTML = "";
        if (scrubberMotionSegments) scrubberMotionSegments.innerHTML = "";

        // 1. Base recordings (cyan)
        recClips.forEach(function (clip) {
            var leftPct = (clip.startSec / 86400) * 100;
            var widthPct = Math.max(0.28, (clip.durationSec / 86400) * 100);

            var block = document.createElement("div");
            block.className = "scrubber-block";
            block.style.left = leftPct + "%";
            block.style.width = widthPct + "%";
            block.title = clip.time + " (" + clip.size_formatted + ")" + (clip.has_motion ? " 🏃 Gerakan" : "");

            block.addEventListener("click", function (e) {
                e.stopPropagation();
                playRecordingClip(clip.origIdx);
            });

            scrubberSegments.appendChild(block);
        });

        // 2. Motion detection highlight intervals (amber/orange)
        if (scrubberMotionSegments && recMotionEvents && recMotionEvents.length > 0) {
            recMotionEvents.forEach(function (ev) {
                var startSec = ev.start_sec || 0;
                var endSec = ev.end_sec || (startSec + 20);
                if (endSec <= startSec) endSec = startSec + 20;

                var leftPct = (startSec / 86400) * 100;
                var widthPct = Math.max(0.35, ((endSec - startSec) / 86400) * 100);

                var mBlock = document.createElement("div");
                mBlock.className = "scrubber-motion-block";
                mBlock.style.left = leftPct + "%";
                mBlock.style.width = widthPct + "%";
                var pctStr = typeof ev.max_change_pct === "number" ? ev.max_change_pct.toFixed(1) + "%" : "";
                mBlock.title = "🏃 Gerakan: " + ev.start_time + " - " + ev.end_time + (pctStr ? " (" + pctStr + ")" : "");

                scrubberMotionSegments.appendChild(mBlock);
            });
        }
    }

    function renderHourFilterPills(hourGroups) {
        if (!hourFilterPills) return;
        hourFilterPills.innerHTML = "";

        var hours = Object.keys(hourGroups).sort();

        // All hours pill
        var allPill = document.createElement("button");
        allPill.className = "hour-pill" + (selectedHourFilter === "all" ? " active" : "");
        allPill.textContent = "🕒 Semua Jam (" + recClips.length + ")";
        allPill.addEventListener("click", function () {
            selectedHourFilter = "all";
            renderTimelineClips();
        });
        hourFilterPills.appendChild(allPill);

        // Motion filter pill
        var motionCount = recClips.filter(function (c) { return !!c.has_motion; }).length;
        if (motionCount > 0) {
            var motionPill = document.createElement("button");
            motionPill.className = "hour-pill motion-pill" + (selectedHourFilter === "motion" ? " active" : "");
            motionPill.innerHTML = "🏃 Gerakan (" + motionCount + ")";
            motionPill.title = "Hanya tampilkan rekaman yang mendeteksi gerakan";
            motionPill.addEventListener("click", function () {
                selectedHourFilter = "motion";
                renderTimelineClips();
            });
            hourFilterPills.appendChild(motionPill);
        }

        // Individual hour pills
        hours.forEach(function (h) {
            var pill = document.createElement("button");
            pill.className = "hour-pill" + (selectedHourFilter === h ? " active" : "");
            pill.textContent = h + ":00 (" + hourGroups[h].length + ")";
            pill.addEventListener("click", function () {
                selectedHourFilter = h;
                renderTimelineClips();
            });
            hourFilterPills.appendChild(pill);
        });
    }

    function renderGroupedClipsList(hourGroups) {
        if (!recTimelineGrouped) return;
        recTimelineGrouped.innerHTML = "";

        var hours = Object.keys(hourGroups).sort();
        var renderedCount = 0;

        hours.forEach(function (h) {
            var clipsToRender = hourGroups[h];
            if (selectedHourFilter === "motion") {
                clipsToRender = hourGroups[h].filter(function (clip) { return !!clip.has_motion; });
                if (clipsToRender.length === 0) return;
            } else if (selectedHourFilter !== "all" && selectedHourFilter !== h) {
                return;
            }

            renderedCount += clipsToRender.length;
            var groupDiv = document.createElement("div");
            groupDiv.className = "timeline-hour-group";
            groupDiv.id = "hour-group-" + h;

            var header = document.createElement("div");
            header.className = "hour-group-header";

            var title = document.createElement("div");
            title.className = "hour-group-title";
            title.innerHTML = '<span>🕒</span> <span>Pukul ' + h + ':00</span>';

            var meta = document.createElement("div");
            meta.className = "hour-group-meta";
            meta.textContent = clipsToRender.length + " rekaman";

            header.appendChild(title);
            header.appendChild(meta);
            groupDiv.appendChild(header);

            var clipsContainer = document.createElement("div");
            clipsContainer.className = "hour-group-clips";

            clipsToRender.forEach(function (clip) {
                var card = document.createElement("button");
                card.className = "timeline-clip-card" + (clip.origIdx === recCurrentClipIndex ? " active" : "");
                if (clip.has_motion) {
                    card.classList.add("has-motion");
                }
                card.id = "rec-clip-" + clip.origIdx;

                var timeRow = document.createElement("div");
                timeRow.className = "clip-time-row";
                timeRow.innerHTML = '<span class="clip-play-icon">▶</span> <span>' + clip.time_label + '</span>';

                if (clip.has_motion) {
                    var badge = document.createElement("span");
                    badge.className = "clip-motion-badge";
                    badge.innerHTML = "🏃 Gerakan";
                    badge.title = "Gerakan terdeteksi dalam rekaman ini";
                    timeRow.appendChild(badge);
                }

                var metaRow = document.createElement("div");
                metaRow.className = "clip-meta-row";
                metaRow.innerHTML = '<span>' + clip.time + '</span><span>' + clip.size_formatted + '</span>';

                card.appendChild(timeRow);
                card.appendChild(metaRow);

                card.addEventListener("click", function () {
                    playRecordingClip(clip.origIdx);
                });

                clipsContainer.appendChild(card);
            });

            groupDiv.appendChild(clipsContainer);
            recTimelineGrouped.appendChild(groupDiv);
        });

        if (renderedCount === 0) {
            if (selectedHourFilter === "motion") {
                recTimelineGrouped.innerHTML = '<div class="timeline-empty">Tidak ada rekaman dengan gerakan terdeteksi pada tanggal ini</div>';
            } else {
                recTimelineGrouped.innerHTML = '<div class="timeline-empty">Tidak ada video pada filter jam ini</div>';
            }
        }
    }

    function playRecordingClip(index, seekOffsetSec) {
        if (index < 0 || index >= recClips.length) return;
        recCurrentClipIndex = index;
        var clip = recClips[index];

        // Update active class on clip cards
        var cards = recTimelineGrouped.querySelectorAll(".timeline-clip-card");
        cards.forEach(function (c) { c.classList.remove("active"); });

        var activeCard = document.getElementById("rec-clip-" + index);
        if (activeCard) {
            activeCard.classList.add("active");
            activeCard.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }

        // Update info bar
        recNowPlayingTitle.textContent = clip.camera_name + " — " + clip.time_label;
        recNowPlayingMeta.textContent = clip.date + " " + clip.time + " (" + clip.size_formatted + ")";

        // Enable download
        recDownloadBtn.disabled = false;
        recDownloadBtn.onclick = function () {
            var downloadUrl = getAuthenticatedUrl(clip.stream_url);
            var a = document.createElement("a");
            a.href = downloadUrl;
            a.download = clip.filename;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
        };

        // Hide empty overlay & reset state
        if (recVideoEmpty) recVideoEmpty.classList.add("hidden");
        if (recVideoError) recVideoError.classList.add("hidden");
        if (recVideoLoading) recVideoLoading.classList.remove("hidden");

        // Load and play video via HTTP Range Requests (chunked streaming like YouTube)
        var streamUrl = getAuthenticatedUrl(clip.stream_url);
        if (recVideoPlayer.src !== streamUrl) {
            recVideoPlayer.src = streamUrl;
        }

        if (seekOffsetSec && seekOffsetSec > 0) {
            var onMeta = function () {
                recVideoPlayer.removeEventListener("loadedmetadata", onMeta);
                try {
                    recVideoPlayer.currentTime = seekOffsetSec;
                } catch (err) {}
            };
            recVideoPlayer.addEventListener("loadedmetadata", onMeta);
        }

        var playPromise = recVideoPlayer.play();
        if (playPromise !== undefined) {
            playPromise.catch(function (e) {
                if (e.name === "AbortError") {
                    // Aborted by browser because user clicked another clip or scrubbed
                    return;
                }
                console.warn("Autoplay was prevented (trying muted):", e);
                recVideoPlayer.muted = true;
                recVideoPlayer.play().catch(function () {});
            });
        }

        // Show scrubber playhead
        if (scrubberPlayhead) {
            var startSec = clip.startSec || timeStrToSeconds(clip.time);
            scrubberPlayhead.style.left = ((startSec / 86400) * 100) + "%";
            scrubberPlayhead.style.display = "block";
        }
    }

    // Video Player State Handlers
    if (recVideoPlayer) {
        recVideoPlayer.addEventListener("waiting", function () {
            if (recVideoLoading) recVideoLoading.classList.remove("hidden");
        });
        recVideoPlayer.addEventListener("playing", function () {
            if (recVideoLoading) recVideoLoading.classList.add("hidden");
            if (recVideoError) recVideoError.classList.add("hidden");
        });
        recVideoPlayer.addEventListener("canplay", function () {
            if (recVideoLoading) recVideoLoading.classList.add("hidden");
        });
        recVideoPlayer.addEventListener("loadeddata", function () {
            if (recVideoLoading) recVideoLoading.classList.add("hidden");
        });
        recVideoPlayer.addEventListener("error", function () {
            if (recVideoLoading) recVideoLoading.classList.add("hidden");
            if (recVideoError) {
                var err = recVideoPlayer.error;
                var msg = "⚠️ Gagal memutar video rekaman.";
                if (err) {
                    if (err.code === 1) {
                        // MEDIA_ERR_ABORTED: Normal when user switches video
                        return;
                    }
                    else if (err.code === 2) msg = "⚠️ Kesalahan jaringan saat memuat video.";
                    else if (err.code === 3) msg = "⚠️ Format video tidak didukung oleh browser.";
                    else if (err.code === 4) msg = "⚠️ File video tidak ditemukan atau izin akses ditolak (401/404).";
                }
                recVideoError.innerHTML = '<span>' + msg + '</span><br><small style="color:#aaa;">Pastikan file ada di server dan container memiliki izin akses baca.</small>';
                recVideoError.classList.remove("hidden");
            }
        });

        // Update Scrubber Playhead Position in real-time as video plays
        recVideoPlayer.addEventListener("timeupdate", function () {
            if (recCurrentClipIndex >= 0 && recCurrentClipIndex < recClips.length && scrubberPlayhead) {
                var clip = recClips[recCurrentClipIndex];
                var baseSec = clip.startSec || timeStrToSeconds(clip.time);
                var currentTotalSec = baseSec + recVideoPlayer.currentTime;
                var pct = (currentTotalSec / 86400) * 100;
                scrubberPlayhead.style.left = pct + "%";
                scrubberPlayhead.style.display = "block";
            }
        });
    }

    // Scrubber Bar Click & Hover Handlers
    if (timelineScrubber) {
        timelineScrubber.addEventListener("mousemove", function (e) {
            if (!scrubberTooltip || recClips.length === 0) return;
            var rect = timelineScrubber.getBoundingClientRect();
            var x = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
            var pct = x / rect.width;
            var hoverSec = Math.floor(pct * 86400);

            scrubberTooltip.textContent = secondsToTimeStr(hoverSec);
            scrubberTooltip.style.left = x + "px";
            scrubberTooltip.style.display = "block";
        });

        timelineScrubber.addEventListener("mouseleave", function () {
            if (scrubberTooltip) scrubberTooltip.style.display = "none";
        });

        timelineScrubber.addEventListener("click", function (e) {
            if (recClips.length === 0) return;
            var rect = timelineScrubber.getBoundingClientRect();
            var x = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
            var targetSec = (x / rect.width) * 86400;

            // Find clip containing or closest to targetSec
            var bestIdx = 0;
            var minDiff = Infinity;
            var exactOffset = 0;

            for (var i = 0; i < recClips.length; i++) {
                var c = recClips[i];
                var s = c.startSec || timeStrToSeconds(c.time);
                var d = c.durationSec || 300;

                if (targetSec >= s && targetSec < s + d) {
                    bestIdx = i;
                    exactOffset = targetSec - s;
                    minDiff = 0;
                    break;
                }

                var diff = Math.abs(targetSec - s);
                if (diff < minDiff) {
                    minDiff = diff;
                    bestIdx = i;
                    exactOffset = 0;
                }
            }

            playRecordingClip(bestIdx, exactOffset);
        });
    }

    // Jump to time input handler
    function handleTimeJump() {
        if (!recTimeJump || recClips.length === 0) return;
        var val = recTimeJump.value; // "HH:MM"
        if (!val) return;

        var targetSec = timeStrToSeconds(val + ":00");
        var bestIdx = 0;
        var minDiff = Infinity;

        for (var i = 0; i < recClips.length; i++) {
            var c = recClips[i];
            var s = c.startSec || timeStrToSeconds(c.time);
            var diff = Math.abs(targetSec - s);
            if (diff < minDiff) {
                minDiff = diff;
                bestIdx = i;
            }
        }

        // Also align hour filter if helpful
        var targetHour = val.split(":")[0];
        if (targetHour) {
            selectedHourFilter = targetHour;
            renderTimelineClips();
        }

        playRecordingClip(bestIdx);
    }

    if (recTimeJumpBtn) {
        recTimeJumpBtn.addEventListener("click", handleTimeJump);
    }
    if (recTimeJump) {
        recTimeJump.addEventListener("keydown", function (e) {
            if (e.key === "Enter") {
                handleTimeJump();
            }
        });
    }

    if (recVideoPlayer) {
        recVideoPlayer.addEventListener("ended", function () {
            if (recAutoplayCheck && recAutoplayCheck.checked) {
                if (recCurrentClipIndex + 1 < recClips.length) {
                    playRecordingClip(recCurrentClipIndex + 1);
                }
            }
        });
    }

    if (calPrevBtn) {
        calPrevBtn.addEventListener("click", function () {
            calMonth--;
            if (calMonth < 0) {
                calMonth = 11;
                calYear--;
            }
            renderCalendar();
        });
    }

    if (calNextBtn) {
        calNextBtn.addEventListener("click", function () {
            calMonth++;
            if (calMonth > 11) {
                calMonth = 0;
                calYear++;
            }
            renderCalendar();
        });
    }

    if (recCameraSelect) {
        recCameraSelect.addEventListener("change", function () {
            recSelectedCamera = this.value;
            recSelectedDate = "";
            loadRecordingDates().then(function () {
                renderCalendar();
                if (recAvailableDates.length > 0) {
                    selectRecordingDate(recAvailableDates[0]);
                } else {
                    recClips = [];
                    renderTimelineClips();
                }
            });
        });
    }

    // ===================================
    // Settings Module (Cameras, Storage, Security)
    // ===================================

    var currentConfigCameras = [];

    // Subtab switching
    function switchSettingsTab(tabName) {
        if (setTabCamerasBtn) setTabCamerasBtn.classList.toggle("active", tabName === "cameras");
        if (setTabStorageBtn) setTabStorageBtn.classList.toggle("active", tabName === "storage");
        if (setTabSecurityBtn) setTabSecurityBtn.classList.toggle("active", tabName === "security");

        if (setPanelCameras) setPanelCameras.classList.toggle("hidden", tabName !== "cameras");
        if (setPanelStorage) setPanelStorage.classList.toggle("hidden", tabName !== "storage");
        if (setPanelSecurity) setPanelSecurity.classList.toggle("hidden", tabName !== "security");
    }

    if (setTabCamerasBtn) {
        setTabCamerasBtn.addEventListener("click", function () { switchSettingsTab("cameras"); });
    }
    if (setTabStorageBtn) {
        setTabStorageBtn.addEventListener("click", function () { switchSettingsTab("storage"); });
    }
    if (setTabSecurityBtn) {
        setTabSecurityBtn.addEventListener("click", function () { switchSettingsTab("security"); });
    }

    // Load Settings
    function loadSettingsData() {
        if (cameraSettingsList) {
            cameraSettingsList.innerHTML = '<div class="settings-loading">Memuat daftar kamera...</div>';
        }
        return authFetch("/api/settings")
            .then(function (res) { return res.json(); })
            .then(function (data) {
                if (!data) return;
                currentConfigCameras = data.cameras || [];
                renderSettingsCameras(currentConfigCameras);
                if (data.storage) renderStorageInfo(data.storage);
                if (data.recording) renderRecordingSettings(data.recording, data.motion);
            })
            .catch(function (err) {
                if (cameraSettingsList) {
                    cameraSettingsList.innerHTML = '<div class="form-error-banner">Gagal memuat pengaturan: ' + (err.message || err) + '</div>';
                }
            });
    }

    // Render Camera List
    function renderSettingsCameras(cameras) {
        if (!cameraSettingsList) return;
        cameraSettingsList.innerHTML = "";

        if (!cameras || cameras.length === 0) {
            cameraSettingsList.innerHTML =
                '<div class="settings-empty">Belum ada kamera yang ditambahkan. Klik tombol <strong>+ Tambah Kamera</strong> di atas untuk menambahkan.</div>';
            return;
        }

        cameras.forEach(function (cam) {
            var item = document.createElement("div");
            item.className = "settings-camera-card";

            var camId = cam.id || cam.ID || "";
            var camName = cam.name || cam.Name || "";
            var camUrl = cam.url || cam.URL || "";
            var camOnvif = cam.onvif_url || cam.OnvifURL || "";
            var isRecording = cam.record !== undefined ? cam.record !== false : (cam.Record !== undefined ? cam.Record !== false : true);
            var modeLabel = camUrl ? "Direct RTSP" : (camOnvif ? "ONVIF" : "Manual");
            var urlDisplay = camUrl || camOnvif || "-";

            item.innerHTML =
                '<div class="cam-info-col">' +
                    '<div class="cam-name-row">' +
                        '<span class="cam-title-text">' + escapeHtml(camName) + '</span>' +
                        '<span class="cam-id-pill">' + escapeHtml(camId) + '</span>' +
                    '</div>' +
                    '<div class="cam-badges-row">' +
                        '<span class="badge-tag badge-mode">📡 ' + modeLabel + '</span>' +
                        (isRecording
                            ? '<span class="badge-tag badge-rec-on">🔴 NVR Merekam</span>'
                            : '<span class="badge-tag badge-rec-off">⚪ NVR Nonaktif</span>') +
                    '</div>' +
                    '<span class="cam-url-preview" title="' + escapeHtml(urlDisplay) + '">' + escapeHtml(urlDisplay) + '</span>' +
                '</div>' +
                '<div class="cam-actions-col">' +
                    '<button class="btn-secondary btn-edit-cam" data-id="' + escapeHtml(camId) + '">✏️ Edit</button>' +
                    '<button class="btn-danger btn-del-cam" data-id="' + escapeHtml(camId) + '">🗑️ Hapus</button>' +
                '</div>';

            cameraSettingsList.appendChild(item);
        });

        // Attach action handlers
        var editBtns = cameraSettingsList.querySelectorAll(".btn-edit-cam");
        editBtns.forEach(function (btn) {
            btn.addEventListener("click", function () {
                var camId = this.getAttribute("data-id");
                var cam = currentConfigCameras.find(function (c) { return (c.id || c.ID) === camId; });
                if (cam) openCameraModal("edit", cam);
            });
        });

        var delBtns = cameraSettingsList.querySelectorAll(".btn-del-cam");
        delBtns.forEach(function (btn) {
            btn.addEventListener("click", function () {
                var camId = this.getAttribute("data-id");
                deleteCamera(camId);
            });
        });
    }

    // Render Storage Info
    function renderStorageInfo(storage) {
        if (!storage) return;

        var pct = Math.round(storage.used_percent || storage.UsedPercent || 0);
        if (storageProgressFill) {
            storageProgressFill.style.width = pct + "%";
            storageProgressFill.classList.remove("warning", "danger");
            if (pct >= 90) {
                storageProgressFill.classList.add("danger");
            } else if (pct >= 75) {
                storageProgressFill.classList.add("warning");
            }
        }

        var usedFormatted = storage.used_formatted || storage.UsedFormatted || "0 B";
        var freeFormatted = storage.free_formatted || storage.FreeFormatted || "0 B";
        var totalFormatted = storage.total_formatted || storage.TotalFormatted || "-";
        var storagePath = storage.path || storage.Path || "-";

        if (storageUsedLabel) storageUsedLabel.textContent = usedFormatted + " Terpakai";
        if (storagePercentLabel) storagePercentLabel.textContent = pct + "%";
        if (storageFreeLabel) storageFreeLabel.textContent = freeFormatted + " Sisa";

        if (storageTotalVal) storageTotalVal.textContent = totalFormatted;
        if (storageUsedVal) storageUsedVal.textContent = usedFormatted;
        if (storageFreeVal) storageFreeVal.textContent = freeFormatted;
        if (storagePathVal) storagePathVal.textContent = storagePath;
    }

    if (btnRefreshStorage) {
        btnRefreshStorage.addEventListener("click", function () {
            btnRefreshStorage.textContent = "⏳";
            authFetch("/api/settings/storage")
                .then(function (res) { return res.json(); })
                .then(function (data) {
                    renderStorageInfo(data);
                    btnRefreshStorage.textContent = "🔄";
                })
                .catch(function () {
                    btnRefreshStorage.textContent = "🔄";
                });
        });
    }

    // Render Recording & Motion Settings
    function renderRecordingSettings(rec, motion) {
        if (rec) {
            var isEnabled = rec.enabled !== undefined ? !!rec.enabled : (rec.Enabled !== undefined ? !!rec.Enabled : true);
            var segMin = rec.segment_minutes !== undefined ? rec.segment_minutes : (rec.SegmentMinutes !== undefined ? rec.SegmentMinutes : 5);
            var retDays = rec.retention_days !== undefined ? rec.retention_days : (rec.RetentionDays !== undefined ? rec.RetentionDays : 7);

            if (nvrEnableCheck) nvrEnableCheck.checked = isEnabled;
            if (nvrSegmentSelect) nvrSegmentSelect.value = String(segMin || 5);
            if (nvrRetentionSelect) nvrRetentionSelect.value = String(retDays || 7);
        }

        if (motion) {
            var isMotionEnabled = motion.enabled !== undefined ? !!motion.enabled : true;
            var sensitivity = motion.sensitivity || "medium";
            if (nvrMotionCheck) nvrMotionCheck.checked = isMotionEnabled;
            if (nvrMotionSensitivitySelect) nvrMotionSensitivitySelect.value = sensitivity;
        }
    }

    // Save NVR & Motion Settings
    if (formNvrSettings) {
        formNvrSettings.addEventListener("submit", function (e) {
            e.preventDefault();
            if (nvrSaveMsg) {
                nvrSaveMsg.textContent = "Menyimpan...";
                nvrSaveMsg.className = "feedback-msg";
            }
            if (btnSaveNvr) btnSaveNvr.disabled = true;

            var isEnabled = nvrEnableCheck.checked;
            var payload = {
                enabled: isEnabled,
                segment_minutes: parseInt(nvrSegmentSelect.value, 10),
                retention_days: parseInt(nvrRetentionSelect.value, 10),
                motion_enabled: nvrMotionCheck ? nvrMotionCheck.checked : true,
                motion_sensitivity: nvrMotionSensitivitySelect ? nvrMotionSensitivitySelect.value : "medium"
            };

            authFetch("/api/settings/recording", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(payload)
            })
            .then(function (res) { return res.json(); })
            .then(function (data) {
                if (btnSaveNvr) btnSaveNvr.disabled = false;
                if (data.success) {
                    if (data.recording) {
                        renderRecordingSettings(data.recording, data.motion);
                    } else {
                        nvrEnableCheck.checked = isEnabled;
                    }
                    if (nvrSaveMsg) {
                        nvrSaveMsg.textContent = "✅ Pengaturan NVR & Deteksi Gerakan berhasil disimpan!";
                        nvrSaveMsg.className = "feedback-msg success";
                        setTimeout(function () { nvrSaveMsg.className = "feedback-msg hidden"; }, 4000);
                    }
                } else {
                    if (nvrSaveMsg) {
                        nvrSaveMsg.textContent = "❌ " + (data.error || "Gagal menyimpan");
                        nvrSaveMsg.className = "feedback-msg error";
                    }
                }
            })
            .catch(function (err) {
                if (btnSaveNvr) btnSaveNvr.disabled = false;
                if (nvrSaveMsg) {
                    nvrSaveMsg.textContent = "❌ " + (err.message || err);
                    nvrSaveMsg.className = "feedback-msg error";
                }
            });
        });
    }

    // Save Security / Password Settings
    if (formSecuritySettings) {
        formSecuritySettings.addEventListener("submit", function (e) {
            e.preventDefault();
            if (secSaveMsg) {
                secSaveMsg.className = "feedback-msg hidden";
            }

            if (secNewPwd.value !== secConfirmPwd.value) {
                if (secSaveMsg) {
                    secSaveMsg.textContent = "❌ Konfirmasi kata sandi baru tidak cocok!";
                    secSaveMsg.className = "feedback-msg error";
                }
                return;
            }

            if (btnSaveSecurity) btnSaveSecurity.disabled = true;

            var payload = {
                current_password: secCurrentPwd.value,
                new_password: secNewPwd.value
            };

            authFetch("/api/settings/security", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(payload)
            })
            .then(function (res) { return res.json(); })
            .then(function (data) {
                if (btnSaveSecurity) btnSaveSecurity.disabled = false;
                if (data.success) {
                    secCurrentPwd.value = "";
                    secNewPwd.value = "";
                    secConfirmPwd.value = "";
                    if (secSaveMsg) {
                        secSaveMsg.textContent = "✅ Kata sandi berhasil diperbarui!";
                        secSaveMsg.className = "feedback-msg success";
                        setTimeout(function () { secSaveMsg.className = "feedback-msg hidden"; }, 4000);
                    }
                } else {
                    if (secSaveMsg) {
                        secSaveMsg.textContent = "❌ " + (data.error || "Gagal mengubah kata sandi");
                        secSaveMsg.className = "feedback-msg error";
                    }
                }
            })
            .catch(function (err) {
                if (btnSaveSecurity) btnSaveSecurity.disabled = false;
                if (secSaveMsg) {
                    secSaveMsg.textContent = "❌ " + (err.message || err);
                    secSaveMsg.className = "feedback-msg error";
                }
            });
        });
    }

    // Camera Modal & Actions
    function openCameraModal(mode, cam) {
        camFormMode.value = mode;
        if (camFormError) camFormError.className = "form-error-banner hidden";
        if (testRtspResult) testRtspResult.className = "test-result-badge hidden";

        if (mode === "add") {
            cameraModalTitle.textContent = "Tambah Kamera Baru";
            camInputName.value = "";
            camInputId.value = "";
            camInputId.readOnly = false;
            camInputUrl.value = "";
            camInputOnvif.value = "";
            camInputUser.value = "";
            camInputPass.value = "";
            camInputAlarmMethod.value = "onvif";
            camInputAlarmOn.value = "";
            camInputRecord.checked = true;
        } else {
            var camId = cam.id || cam.ID || "";
            var camName = cam.name || cam.Name || "";
            var camUrl = cam.url || cam.URL || "";
            var camOnvif = cam.onvif_url || cam.OnvifURL || "";
            var camUser = cam.username || cam.Username || "";
            var camPass = cam.password || cam.Password || "";
            var camRecord = cam.record !== undefined ? cam.record !== false : (cam.Record !== undefined ? cam.Record !== false : true);
            var alarmMethod = (cam.alarm && (cam.alarm.method || cam.alarm.Method)) || (cam.Alarm && (cam.Alarm.method || cam.Alarm.Method)) || "onvif";
            var alarmOn = (cam.alarm && (cam.alarm.on_url || cam.alarm.OnURL)) || (cam.Alarm && (cam.Alarm.on_url || cam.Alarm.OnURL)) || "";

            cameraModalTitle.textContent = "Edit Kamera: " + (camName || camId);
            camInputName.value = camName;
            camInputId.value = camId;
            camInputId.readOnly = true;
            camInputUrl.value = camUrl;
            camInputOnvif.value = camOnvif;
            camInputUser.value = camUser;
            camInputPass.value = camPass;
            camInputAlarmMethod.value = alarmMethod;
            camInputAlarmOn.value = alarmOn;
            camInputRecord.checked = camRecord;
        }

        cameraModalOverlay.classList.remove("hidden");
    }

    function closeCameraModal() {
        if (cameraModalOverlay) cameraModalOverlay.classList.add("hidden");
    }

    if (btnAddCamera) {
        btnAddCamera.addEventListener("click", function () {
            openCameraModal("add");
        });
    }

    if (cameraModalClose) {
        cameraModalClose.addEventListener("click", closeCameraModal);
    }
    if (btnCancelCam) {
        btnCancelCam.addEventListener("click", closeCameraModal);
    }

    // Test RTSP
    if (btnTestRtsp) {
        btnTestRtsp.addEventListener("click", function () {
            var url = camInputUrl.value.trim();
            var onvifUrl = camInputOnvif.value.trim();

            if (!url && !onvifUrl) {
                testRtspResult.textContent = "❌ Masukkan URL RTSP atau ONVIF terlebih dahulu";
                testRtspResult.className = "test-result-badge error";
                return;
            }

            btnTestRtsp.disabled = true;
            btnTestRtsp.innerHTML = "<span>⏳</span> Menguji koneksi...";
            testRtspResult.className = "test-result-badge hidden";

            var payload = {
                url: url,
                onvif_url: onvifUrl,
                username: camInputUser.value.trim(),
                password: camInputPass.value.trim()
            };

            authFetch("/api/settings/cameras/test", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(payload)
            })
            .then(function (res) { return res.json(); })
            .then(function (data) {
                btnTestRtsp.disabled = false;
                btnTestRtsp.innerHTML = "<span>🔍</span> Uji Koneksi RTSP";

                if (data.success) {
                    testRtspResult.textContent = "✅ " + data.message;
                    testRtspResult.className = "test-result-badge success";
                } else {
                    testRtspResult.textContent = "❌ " + (data.error || "Gagal terhubung");
                    testRtspResult.className = "test-result-badge error";
                }
            })
            .catch(function (err) {
                btnTestRtsp.disabled = false;
                btnTestRtsp.innerHTML = "<span>🔍</span> Uji Koneksi RTSP";
                testRtspResult.textContent = "❌ " + (err.message || err);
                testRtspResult.className = "test-result-badge error";
            });
        });
    }

    // Submit Camera Form
    if (cameraForm) {
        cameraForm.addEventListener("submit", function (e) {
            e.preventDefault();
            if (camFormError) camFormError.className = "form-error-banner hidden";

            var mode = camFormMode.value;
            var name = camInputName.value.trim();
            var id = camInputId.value.trim();
            var url = camInputUrl.value.trim();
            var onvifUrl = camInputOnvif.value.trim();

            if (!name) {
                showCamError("Nama kamera wajib diisi");
                return;
            }
            if (!url && !onvifUrl) {
                showCamError("URL RTSP atau ONVIF wajib diisi");
                return;
            }

            var recVal = camInputRecord.checked;
            var camData = {
                name: name,
                id: id,
                url: url,
                onvif_url: onvifUrl,
                username: camInputUser.value.trim(),
                password: camInputPass.value.trim(),
                record: recVal,
                alarm: {
                    method: camInputAlarmMethod.value,
                    on_url: camInputAlarmOn.value.trim()
                }
            };

            var endpoint = "/api/settings/cameras";
            var httpMethod = "POST";
            if (mode === "edit") {
                endpoint = "/api/settings/cameras/" + encodeURIComponent(id);
                httpMethod = "PUT";
            }

            if (btnSubmitCam) btnSubmitCam.disabled = true;

            authFetch(endpoint, {
                method: httpMethod,
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(camData)
            })
            .then(function (res) { return res.json(); })
            .then(function (data) {
                if (btnSubmitCam) btnSubmitCam.disabled = false;
                if (data.success) {
                    closeCameraModal();
                    loadSettingsData();
                    pollStatus();
                } else {
                    showCamError(data.error || "Gagal menyimpan kamera");
                }
            })
            .catch(function (err) {
                if (btnSubmitCam) btnSubmitCam.disabled = false;
                showCamError(err.message || err);
            });
        });
    }

    function showCamError(msg) {
        if (camFormError) {
            camFormError.textContent = msg;
            camFormError.className = "form-error-banner";
        }
    }

    // Delete Camera
    function deleteCamera(camId) {
        if (!confirm("Apakah Anda yakin ingin menghapus kamera " + camId + "? Perekaman dan live stream untuk kamera ini akan dihentikan.")) {
            return;
        }

        authFetch("/api/settings/cameras/" + encodeURIComponent(camId), {
            method: "DELETE"
        })
        .then(function (res) { return res.json(); })
        .then(function (data) {
            if (data.success) {
                loadSettingsData();
                pollStatus();
            } else {
                alert("Gagal menghapus kamera: " + (data.error || "Unknown error"));
            }
        })
        .catch(function (err) {
            alert("Gagal menghapus kamera: " + (err.message || err));
        });
    }

    function escapeHtml(str) {
        if (!str) return "";
        return String(str)
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#039;");
    }

    // ===================================
    // Dashboard Loop & Lifecycle
    // ===================================

    function stopAllDashboardLoops() {
        if (pollTimer) {
            clearInterval(pollTimer);
            pollTimer = null;
        }
        for (var id in thumbTimers) {
            clearInterval(thumbTimers[id]);
        }
        thumbTimers = {};
        if (activeModalCam) {
            closeModal();
        }
        if (recVideoPlayer) {
            recVideoPlayer.pause();
        }
    }

    function startDashboard() {
        stopAllDashboardLoops();

        fetchCameras().then(function (cameras) {
            if (!cameras) {
                grid.innerHTML =
                    '<div class="loading">Memuat kamera atau menunggu server...</div>';
                return;
            }

            currentCameras = cameras;

            grid.innerHTML = "";
            cameras.forEach(function (cam) {
                var card = createCameraCard(cam);
                grid.appendChild(card);
                updateCameraStatus(cam);
            });

            pollTimer = setInterval(pollStatus, POLL_INTERVAL);
        });

        if (window.location.hash === "#rekaman" || window.location.search.indexOf("tab=rekaman") !== -1) {
            switchView("recordings");
        } else if (window.location.hash === "#pengaturan" || window.location.search.indexOf("tab=pengaturan") !== -1) {
            switchView("settings");
        } else {
            setTimeout(initRecordingsView, 500);
        }
    }

    function pollStatus() {
        fetchCameras().then(function (cameras) {
            if (!cameras) return;

            var currentIds = currentCameras.map(function (c) { return c.id; }).sort().join(",");
            var newIds = cameras.map(function (c) { return c.id; }).sort().join(",");

            if (currentIds !== newIds) {
                currentCameras = cameras;
                grid.innerHTML = "";
                cameras.forEach(function (cam) {
                    var card = createCameraCard(cam);
                    grid.appendChild(card);
                });
            }

            currentCameras = cameras;

            cameras.forEach(function (cam) {
                updateCameraStatus(cam);
            });
        });
    }

    // Auto-switch to recordings or settings if requested in URL hash or query param
    window.addEventListener("hashchange", function () {
        if (window.location.hash === "#rekaman") {
            switchView("recordings");
        } else if (window.location.hash === "#pengaturan") {
            switchView("settings");
        } else if (window.location.hash === "#live" || !window.location.hash) {
            switchView("live");
        }
    });

    // Start App by checking Auth
    checkAuthAndStart();
})();
