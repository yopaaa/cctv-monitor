package recorder

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cctv-monitor/internal/config"
)

// FileInfoItem holds path and timestamp for sorting and cleanup.
type FileInfoItem struct {
	Path    string
	ModTime time.Time
	Size    int64
}

// Cleaner handles storage retention and emergency disk space cleanup.
type Cleaner struct {
	cfg *config.Config
}

// NewCleaner creates a new storage cleaner.
func NewCleaner(cfg *config.Config) *Cleaner {
	return &Cleaner{cfg: cfg}
}

// Start runs the periodic retention cleaner in background.
func (c *Cleaner) Start(ctx context.Context) {
	// Run once at startup after a brief delay
	go func() {
		select {
		case <-time.After(10 * time.Second):
			c.RunCleanup()
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RunCleanup()
			}
		}
	}()
}

// RunCleanup performs age-based and threshold-based storage cleanup.
func (c *Cleaner) RunCleanup() {
	dir := c.cfg.Recordings.Dir
	if dir == "" {
		return
	}
	cleanDir := filepath.Clean(dir)
	if _, err := os.Stat(cleanDir); os.IsNotExist(err) {
		return
	}

	retentionDays := c.cfg.Recordings.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	now := time.Now()

	var allFiles []FileInfoItem
	var deletedCount int
	var freedBytes int64

	// Step 1: Scan all video clips in recordings dir
	_ = filepath.WalkDir(cleanDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".mp4") && !strings.HasSuffix(name, ".ts") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		itemTime := info.ModTime()

		// Protect very recent files (written within last 15 mins) from cleanup
		if now.Sub(itemTime) < 15*time.Minute {
			return nil
		}

		// Check if file exceeds retention period
		if itemTime.Before(cutoff) {
			if err := os.Remove(path); err == nil {
				deletedCount++
				freedBytes += info.Size()
				return nil
			}
		}

		allFiles = append(allFiles, FileInfoItem{
			Path:    path,
			ModTime: itemTime,
			Size:    info.Size(),
		})
		return nil
	})

	if deletedCount > 0 {
		log.Printf("[CLEANER] Auto-retention: removed %d expired files (%s freed, older than %d days)",
			deletedCount, formatBytes(freedBytes), retentionDays)
	}

	// Step 2: Emergency disk threshold cleanup (if free < 5GB or usage > 90%)
	disk, err := GetDiskUsage(cleanDir)
	if err == nil {
		minFreeBytes := uint64(5 * 1024 * 1024 * 1024) // 5 GB
		if (disk.FreeBytes < minFreeBytes || disk.UsedPercent > 90.0) && len(allFiles) > 0 {
			log.Printf("[CLEANER] Disk threshold triggered: %.1f%% used, %s free. Purging oldest recordings...",
				disk.UsedPercent, disk.FreeFormatted)

			// Sort oldest files first
			sort.Slice(allFiles, func(i, j int) bool {
				return allFiles[i].ModTime.Before(allFiles[j].ModTime)
			})

			emergencyDeleted := 0
			var emergencyFreed int64

			for _, f := range allFiles {
				if err := os.Remove(f.Path); err == nil {
					emergencyDeleted++
					emergencyFreed += f.Size
				}

				// Re-check disk stats every 5 files
				if emergencyDeleted%5 == 0 {
					currentDisk, checkErr := GetDiskUsage(cleanDir)
					if checkErr == nil && currentDisk.FreeBytes >= minFreeBytes && currentDisk.UsedPercent <= 88.0 {
						break
					}
				}
			}

			if emergencyDeleted > 0 {
				log.Printf("[CLEANER] Emergency cleanup: removed %d oldest clips (%s freed)",
					emergencyDeleted, formatBytes(emergencyFreed))
			}
		}
	}

	// Step 3: Remove empty subdirectories if any
	cleanEmptyDirs(cleanDir)
}

func cleanEmptyDirs(baseDir string) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			subPath := filepath.Join(baseDir, entry.Name())
			subEntries, err := os.ReadDir(subPath)
			if err == nil && len(subEntries) == 0 {
				_ = os.Remove(subPath)
			}
		}
	}
}
