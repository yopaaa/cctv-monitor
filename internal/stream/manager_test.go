package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cctv-monitor/internal/config"
)

func TestGetLatestCompletedSegmentPlaylist(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_stream_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := NewManager(&config.Config{})

	// Create segments where alphabetical sort would fail: seg_999.ts vs seg_1000.ts vs seg_1001.ts
	// seg_1000.ts comes alphabetically before seg_999.ts, but chronologically after!
	seg999 := filepath.Join(tmpDir, "seg_999.ts")
	seg1000 := filepath.Join(tmpDir, "seg_1000.ts")
	seg1001 := filepath.Join(tmpDir, "seg_1001.ts")

	_ = os.WriteFile(seg999, []byte("segment 999 content"), 0644)
	_ = os.WriteFile(seg1000, []byte("segment 1000 content"), 0644)
	_ = os.WriteFile(seg1001, []byte("segment 1001 content"), 0644)

	// Write index.m3u8 representing active playlist:
	// seg_1001 is currently writing, seg_1000 is latest completed!
	m3u8Content := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:999
#EXTINF:2.0,
seg_999.ts
#EXTINF:2.0,
seg_1000.ts
#EXTINF:2.0,
seg_1001.ts
`
	_ = os.WriteFile(filepath.Join(tmpDir, "index.m3u8"), []byte(m3u8Content), 0644)

	target := mgr.getLatestCompletedSegment(tmpDir)
	if target != seg1000 {
		t.Errorf("Expected latest completed segment %s, got %s", seg1000, target)
	}
}

func TestGetLatestCompletedSegmentFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_stream_fallback_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := NewManager(&config.Config{})

	// No index.m3u8 — test fallback to ModTime
	seg1 := filepath.Join(tmpDir, "seg_001.ts")
	seg2 := filepath.Join(tmpDir, "seg_002.ts")

	now := time.Now()
	_ = os.WriteFile(seg1, []byte("segment 1"), 0644)
	_ = os.Chtimes(seg1, now.Add(-10*time.Second), now.Add(-10*time.Second))

	_ = os.WriteFile(seg2, []byte("segment 2"), 0644)
	_ = os.Chtimes(seg2, now.Add(-2*time.Second), now.Add(-2*time.Second))

	target := mgr.getLatestCompletedSegment(tmpDir)
	if target != seg1 {
		t.Errorf("Expected completed segment %s, got %s", seg1, target)
	}
}

func TestCleanStaleSegments(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_stream_clean_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	seg1 := filepath.Join(tmpDir, "seg_001.ts")
	seg2 := filepath.Join(tmpDir, "seg_002.ts")
	m3u8 := filepath.Join(tmpDir, "index.m3u8")
	thumb := filepath.Join(tmpDir, "thumbnail.jpg")

	_ = os.WriteFile(seg1, []byte("data"), 0644)
	_ = os.WriteFile(seg2, []byte("data"), 0644)
	_ = os.WriteFile(m3u8, []byte("data"), 0644)
	_ = os.WriteFile(thumb, []byte("keep this image"), 0644)

	cleanStaleSegments(tmpDir)

	if _, err := os.Stat(seg1); !os.IsNotExist(err) {
		t.Errorf("seg_001.ts was not cleaned")
	}
	if _, err := os.Stat(seg2); !os.IsNotExist(err) {
		t.Errorf("seg_002.ts was not cleaned")
	}
	if _, err := os.Stat(m3u8); !os.IsNotExist(err) {
		t.Errorf("index.m3u8 was not cleaned")
	}
	if _, err := os.Stat(thumb); os.IsNotExist(err) {
		t.Errorf("thumbnail.jpg should be preserved")
	}
}
