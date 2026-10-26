package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DiskUsage holds storage capacity information.
type DiskUsage struct {
	Path           string  `json:"path"`
	TotalBytes     uint64  `json:"total_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	TotalFormatted string  `json:"total_formatted"`
	FreeFormatted  string  `json:"free_formatted"`
	UsedFormatted  string  `json:"used_formatted"`
}

// GetDiskUsage retrieves filesystem storage capacity for the given path.
func GetDiskUsage(path string) (DiskUsage, error) {
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(cleanPath, 0755); err != nil {
		// If creating directory fails, try parent
		cleanPath = filepath.Dir(cleanPath)
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(cleanPath, &stat); err != nil {
		return DiskUsage{}, fmt.Errorf("statfs %s: %w", cleanPath, err)
	}

	// Calculate total and free bytes using Bsize (or Frsize where applicable)
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize // Available to unprivileged user
	var used uint64
	if total >= free {
		used = total - free
	}

	var percent float64
	if total > 0 {
		percent = float64(used) / float64(total) * 100.0
	}

	return DiskUsage{
		Path:           cleanPath,
		TotalBytes:     total,
		FreeBytes:      free,
		UsedBytes:      used,
		UsedPercent:    percent,
		TotalFormatted: formatBytes(int64(total)),
		FreeFormatted:  formatBytes(int64(free)),
		UsedFormatted:  formatBytes(int64(used)),
	}, nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
