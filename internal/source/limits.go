package source

import (
	"fmt"

	"github.com/alternayte/speccy/internal/kernel"
)

// REQ-009: in hosted mode one file is at most 10 MB and one bundle at most 50 MB.
// Admin configuration of these limits arrives with the admin screens (M8).
const (
	MaxFileBytes   = 10 << 20
	MaxBundleBytes = 50 << 20
)

// CheckLimits returns an error when a file or the whole bundle is over the REQ-009 limits.
func CheckLimits(files []File) error {
	var total int64
	for _, f := range files {
		n := int64(len(f.Content))
		if n > MaxFileBytes {
			return kernel.TooLarge("file_too_large", "%s is %s. The limit for one file is %s. Make the file smaller or split it.", f.Path, mb(n), mb(MaxFileBytes))
		}
		total += n
	}
	if total > MaxBundleBytes {
		return kernel.TooLarge("bundle_too_large", "The bundle is %s. The limit for one bundle is %s. Remove or shrink some assets.", mb(total), mb(MaxBundleBytes))
	}
	return nil
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
