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
    var THUMB_INTERVAL = 10000;
    var RETRY_DELAY = 3000;

    // DOM Elements — Dashboard & Navigation
    var navLiveBtn = document.getElementById("nav-live-btn");
    var navRecordingsBtn = document.getElementById("nav-recordings-btn");
    var liveView = document.getElementById("live-view");
    var recordingsView = document.getElementById("recordings-view");
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

    var timelineDateTag = document.getElementById("timeline-date-tag");
    var recTimelineGrid = document.getElementById("rec-timeline-grid");

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
    var recCurrentClipIndex = -1;

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

        thumbTimers[camId] = setInterval(function () {
            var thumb = document.getElementById("thumb-" + camId);
            if (thumb) {
                thumb.src = getAuthenticatedUrl("/streams/" + camId + "/thumbnail.jpg?t=" + Date.now());
            }
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
    // View Switcher (Live vs Rekaman)
    // ===================================

    function switchView(view) {
        currentView = view;
        if (view === "live") {
            navLiveBtn.classList.add("active");
            navRecordingsBtn.classList.remove("active");
            liveView.classList.remove("hidden");
            recordingsView.classList.add("hidden");
            if (gridSelectorGroup) gridSelectorGroup.style.display = "";
            if (recVideoPlayer) recVideoPlayer.pause();
        } else {
            navLiveBtn.classList.remove("active");
            navRecordingsBtn.classList.add("active");
            liveView.classList.add("hidden");
            recordingsView.classList.remove("hidden");
            if (gridSelectorGroup) gridSelectorGroup.style.display = "none";
            if (activeModalCam) closeModal();
            initRecordingsView();
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

        var parts = dateISO.split("-");
        var displayLabel = parts.length === 3 ? parts[2] + " " + MONTH_NAMES[parseInt(parts[1], 10) - 1] + " " + parts[0] : dateISO;

        recSelectedDateLabel.textContent = displayLabel;
        timelineDateTag.textContent = displayLabel;

        renderCalendar();
        loadTimelineClips(recSelectedCamera, dateISO);
    }

    function loadTimelineClips(camID, dateISO) {
        recTimelineGrid.innerHTML = '<div class="timeline-empty">Memuat rekaman...</div>';
        recCurrentClipIndex = -1;

        var url = "/api/recordings/timeline?camera=" + encodeURIComponent(camID) + "&date=" + encodeURIComponent(dateISO);
        authFetch(url)
            .then(function (resp) { return resp.json(); })
            .then(function (clips) {
                recClips = clips || [];
                recTotalClipsLabel.textContent = recClips.length + " klip";
                renderTimelineClips();
            })
            .catch(function (err) {
                recTimelineGrid.innerHTML = '<div class="timeline-empty">Gagal memuat: ' + err.message + '</div>';
            });
    }

    function renderTimelineClips() {
        recTimelineGrid.innerHTML = "";

        if (recClips.length === 0) {
            recTimelineGrid.innerHTML = '<div class="timeline-empty">Tidak ada video rekaman pada tanggal ini</div>';
            return;
        }

        recClips.forEach(function (clip, idx) {
            var btn = document.createElement("button");
            btn.className = "timeline-clip-btn" + (idx === recCurrentClipIndex ? " active" : "");
            btn.id = "rec-clip-" + idx;

            var timeEl = document.createElement("span");
            timeEl.className = "clip-time";
            timeEl.innerHTML = "▶ " + clip.time_label;

            var metaEl = document.createElement("span");
            metaEl.className = "clip-meta";
            metaEl.textContent = clip.time + " • " + clip.size_formatted;

            btn.appendChild(timeEl);
            btn.appendChild(metaEl);

            btn.addEventListener("click", function () {
                playRecordingClip(idx);
            });

            recTimelineGrid.appendChild(btn);
        });
    }

    function playRecordingClip(index) {
        if (index < 0 || index >= recClips.length) return;
        recCurrentClipIndex = index;
        var clip = recClips[index];

        var buttons = recTimelineGrid.querySelectorAll(".timeline-clip-btn");
        buttons.forEach(function (b) { b.classList.remove("active"); });

        var activeBtn = document.getElementById("rec-clip-" + index);
        if (activeBtn) {
            activeBtn.classList.add("active");
            activeBtn.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }

        recNowPlayingTitle.textContent = clip.camera_name + " — " + clip.time_label;
        recNowPlayingMeta.textContent = clip.date + " " + clip.time + " (" + clip.size_formatted + ")";

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

        recVideoEmpty.classList.add("hidden");

        // Progressive range streaming via HTTP 206
        var streamUrl = getAuthenticatedUrl(clip.stream_url);
        recVideoPlayer.src = streamUrl;
        recVideoPlayer.load();
        recVideoPlayer.play().catch(function (e) {
            console.log("Autoplay was prevented:", e);
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

    // Start App by checking Auth
    checkAuthAndStart();
})();
