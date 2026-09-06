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

    // DOM Elements — Dashboard
    var grid = document.getElementById("camera-grid");
    var gridSelect = document.getElementById("grid-select");
    var statusIndicator = document.getElementById("status-indicator");
    var userProfile = document.getElementById("user-profile");
    var userEmailBadge = document.getElementById("user-email-badge");
    var logoutBtn = document.getElementById("logout-btn");

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

    // State
    var currentCameras = [];
    var activeModalCam = null;
    var activeHls = null;
    var thumbTimers = {};
    var pollTimer = null;
    var alarmActive = false;
    var alarmLoading = false;
    var currentEmail = "admin";
    var authToken = localStorage.getItem("cctv_token") || "";

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

    modalFullscreenBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        var container = document.querySelector(".modal-video-wrapper");
        if (container.requestFullscreen) {
            container.requestFullscreen();
        } else if (container.webkitRequestFullscreen) {
            container.webkitRequestFullscreen();
        }
    });

    modalAlarmBtn.addEventListener("click", function (e) {
        e.stopPropagation();
        toggleAlarm();
    });

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
