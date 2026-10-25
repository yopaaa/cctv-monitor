# 📹 CCTV Monitor (Golang)

Aplikasi live monitoring CCTV modern, ringan, dan **100% kompatibel dengan Cloudflare Zero Trust Tunnel** tanpa error WebSocket (`WSS`).

Dibuat menggunakan **Golang** dan **FFmpeg** dengan UI dashboard web responsif yang di-embed langsung ke dalam single binary.

---

## ✨ Fitur Utama

- 🚫 **Zero WebSocket (Bebas Error WSS)**: Menggunakan HLS (*HTTP Live Streaming*) murni HTTP GET — lancar di-tunnel lewat Cloudflare Zero Trust / reverse proxy.
- 🔒 **Single-Password Security**: Form login simpel hanya meminta Password. Email/username otomatis dibaca dari URL path (`/user@email.com`) atau query parameter (`?email=...`).
- ⚡ **Hemat Bandwidth (Thumbnail Mode)**:
  - Default: Grid menampilkan snapshot thumbnail JPEG (di-refresh tiap 10 detik). Bandwidth nyaris 0 Mbps saat standby.
  - Live Stream: Klik kamera untuk membuka modal live video HLS layar penuh.
- 📼 **Pemutar Rekaman (Playback NVR Shinobi)**:
  - Tampilan Desktop: Kalender interaktif di kiri & Garis waktu rekaman (timeline klip) di kanan.
  - **Streaming Bebas Lag (Kaya YouTube)**: Menggunakan HTTP Range Requests (`206 Partial Content`) mengirim data video bertahap sesuai kebutuhan buffer.
  - Fitur Auto-Next: Otomatis memutar klip berikutnya saat rekaman selesai.
  - Dukungan Storage Shinobi: Membaca struktur hirarki `/mnt/nvr_storage/{groupKey}/{monitorId}/*.mp4` maupun folder flat.
- 🔊 **Tombol Alarm / Siren Kamera**: Kontrol alarm CCTV (kompatibel V380 / XiongMai / ONVIF Relay).
- 📷 **Instant Snapshot & Fullscreen**: Ambil screenshot resolusi penuh dan mode layar penuh per kamera.
- 🔄 **Auto-Reconnect**: Otomatis menyambungkan ulang stream jika kamera offline / mati lampu.
- 📦 **Single Binary & Docker Ready**: Web UI ter-embed di binary Go (`//go:embed`), siap dijalankan via Docker Compose.

---

## 🚀 Cara Menjalankan

### Opsi 1: Menggunakan Docker Compose (Direkomendasikan)

1. Buat file `config.yaml` (atau copy dari `config.example.yaml`):
   ```bash
   cp config.example.yaml config.yaml
   ```

2. Edit `config.yaml` dan sesuaikan IP kamera serta password dashboard Anda.

3. Jalankan container:
   ```bash
   docker compose up -d
   ```

4. Buka di browser:
   ```
   http://localhost:8080/?email=admin@cctv.local
   ```

---

### Opsi 2: Menjalankan Binary Langsung (Tanpa Docker)

**Prasyarat:**
- `Go 1.25+`
- `FFmpeg` terpasang di sistem (`sudo apt install ffmpeg` / `sudo dnf install ffmpeg`)

**Langkah:**
```bash
# 1. Clone repository
git clone https://github.com/llabbs/cctv-monitor.git
cd cctv-monitor

# 2. Setup config
cp config.example.yaml config.yaml
# Edit config.yaml sesuai kamera Anda

# 3. Build & Run
go build -o cctv-monitor .
./cctv-monitor -config config.yaml
```

---

## 🌐 Publish Online via Cloudflare Zero Trust

Untuk mengakses CCTV Anda dari internet secara aman dan gratis:

```bash
cloudflared tunnel --url http://localhost:8080
```

Buka URL Cloudflare Anda:
```
https://cctv.domainanda.com/yopa@gmail.com
```
Masukkan password Anda, dan dashboard live monitoring langsung siap digunakan!

---

## ⚙️ Konfigurasi (`config.yaml`)

```yaml
server:
  port: 8080
  streams_dir: "./streams"

# Pengaturan Login Dashboard
auth:
  enabled: true
  password: "password_rahasia_anda"  # Password untuk masuk ke web
  email: "admin@cctv.local"          # Default email/nama

cameras:
  # Kamera 1: Contoh V380 / RTSP biasa
  - id: cam1
    name: "Kamera Depan"
    url: "rtsp://admin:password@192.168.1.100:554/live/ch00_1"  # Sub-stream (hemat bandwidth)
    username: "admin"
    password: "password"
    onvif_url: "http://192.168.1.100:80"

    # Kontrol Alarm / Siren Kamera
    alarm:
      method: "http_get"
      on_url: "http://192.168.1.100/cgi-bin/hi3510/param.cgi?cmd=setalarmsoundattr&-alarm_mode=1"
      off_url: "http://192.168.1.100/cgi-bin/hi3510/param.cgi?cmd=setalarmsoundattr&-alarm_mode=0"

  # Kamera 2: Contoh ONVIF Auto-Discovery
  # - id: cam2
  #   name: "Kamera Belakang"
  #   onvif_url: "http://192.168.1.101:80"
  #   username: "admin"
  #   password: "password"
```

---

## 🐳 Update Image Docker

Untuk memperbarui ke versi terbaru dari Docker Hub:

```bash
docker compose pull && docker compose up -d
```

---

## 🛠️ Tech Stack

- **Backend**: Golang (Standard Library `net/http`, `embed`, SOAP ONVIF client)
- **Streaming Engine**: FFmpeg (`-c:v copy` remuxing to HLS, CPU < 1%)
- **Frontend**: HTML5, Vanilla JavaScript, CSS3 (Dark Theme), [HLS.js](https://github.com/video-dev/hls.js)
- **Container**: Docker Multi-Stage Build (Alpine Linux)

---

## 📄 Lisensi

MIT License © 2026 llabbs
