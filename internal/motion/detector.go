package motion

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"
	"time"

	"cctv-monitor/internal/config"
)

const (
	sampleGridWidth  = 64
	sampleGridHeight = 36
)

// SensitivityConfig defines pixel delta threshold and overall area change percentage.
type SensitivityConfig struct {
	PixelDeltaThreshold uint8
	MinChangePercentage float64
}

var sensitivities = map[string]SensitivityConfig{
	"high": {
		PixelDeltaThreshold: 20,
		MinChangePercentage: 2.5,
	},
	"medium": {
		PixelDeltaThreshold: 25,
		MinChangePercentage: 4.0,
	},
	"low": {
		PixelDeltaThreshold: 35,
		MinChangePercentage: 7.0,
	},
}

// Detector performs lightweight, low-CPU pixel difference motion detection across camera frames.
type Detector struct {
	cfg         *config.Config
	eventMgr    *EventManager
	prevFrames  map[string][]uint8
	sensitivity string
	mu          sync.Mutex
}

// NewDetector creates a new motion Detector instance.
func NewDetector(cfg *config.Config, eventMgr *EventManager) *Detector {
	sens := "medium"
	if cfg != nil && cfg.Motion.Sensitivity != "" {
		sens = cfg.Motion.Sensitivity
	}
	return &Detector{
		cfg:         cfg,
		eventMgr:    eventMgr,
		prevFrames:  make(map[string][]uint8),
		sensitivity: sens,
	}
}

// SetSensitivity updates the motion detection sensitivity ("low", "medium", "high").
func (d *Detector) SetSensitivity(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := sensitivities[s]; ok {
		d.sensitivity = s
	}
}

// GetSensitivity returns the current sensitivity level.
func (d *Detector) GetSensitivity() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sensitivity
}

// EventManager returns the underlying event manager.
func (d *Detector) EventManager() *EventManager {
	return d.eventMgr
}

// ProcessFrame analyzes a frame image (e.g. thumbnail.jpg) and returns whether motion was detected.
func (d *Detector) ProcessFrame(camID, imagePath string) (bool, float64) {
	if d.cfg != nil && !d.cfg.Motion.Enabled {
		return false, 0
	}

	pixels, err := sampleGrayscale(imagePath, sampleGridWidth, sampleGridHeight)
	if err != nil {
		return false, 0
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	prev, exists := d.prevFrames[camID]
	d.prevFrames[camID] = pixels

	if !exists || len(prev) != len(pixels) {
		// First frame acts as baseline
		return false, 0
	}

	sens := sensitivities[d.sensitivity]

	// Compute pixel differences
	changedPixels := 0
	totalPixels := len(pixels)

	for i := 0; i < totalPixels; i++ {
		diff := int(pixels[i]) - int(prev[i])
		if diff < 0 {
			diff = -diff
		}
		if uint8(diff) > sens.PixelDeltaThreshold {
			changedPixels++
		}
	}

	changePct := float64(changedPixels) / float64(totalPixels) * 100.0

	if changePct >= sens.MinChangePercentage {
		d.eventMgr.RecordMotion(camID, time.Now(), changePct)
		return true, changePct
	}

	return false, changePct
}

// sampleGrayscale downsamples an image from disk to a small fixed-size grayscale grid.
func sampleGrayscale(path string, width, height int) ([]uint8, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	bw := bounds.Dx()
	bh := bounds.Dy()
	if bw <= 0 || bh <= 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}

	pixels := make([]uint8, width*height)

	for y := 0; y < height; y++ {
		sy := bounds.Min.Y + (y * bh / height)
		rowOffset := y * width
		for x := 0; x < width; x++ {
			sx := bounds.Min.X + (x * bw / width)
			r, g, b, _ := img.At(sx, sy).RGBA()
			// RGBA() returns [0, 65535] — shift by 8 bits to get standard 8-bit [0, 255]
			r8 := r >> 8
			g8 := g >> 8
			b8 := b >> 8
			gray := uint8((r8*299 + g8*587 + b8*114) / 1000)
			pixels[rowOffset+x] = gray
		}
	}

	return pixels, nil
}
