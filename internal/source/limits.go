package source

import (
	"fmt"

	"github.com/alternayte/speccy/internal/kernel"
)

// REQ-009: by default one file is at most 10 MB and one bundle at most 50 MB. An admin can
// change both in hosted mode, up to the ceilings below.
const (
	MaxFileBytes   = 10 << 20
	MaxBundleBytes = 50 << 20
	// CeilingFileBytes and CeilingBundleBytes bound what an admin can set. A request body is
	// at most 60 MB, so one upload or import stays under it.
	CeilingFileBytes   = 50 << 20
	CeilingBundleBytes = 500 << 20
)

// Limits are the REQ-009 limits in force.
type Limits struct {
	FileBytes   int64
	BundleBytes int64
}

// DefaultLimits are the REQ-009 defaults.
var DefaultLimits = Limits{FileBytes: MaxFileBytes, BundleBytes: MaxBundleBytes}

// CheckLimits returns an error when a file or the whole bundle is over the limits.
func CheckLimits(files []File, lim Limits) error {
	var total int64
	for _, f := range files {
		n := int64(len(f.Content))
		if n > lim.FileBytes {
			return kernel.TooLarge("file_too_large", "%s is %s. The limit for one file is %s. Make the file smaller or split it.", f.Path, mb(n), mb(lim.FileBytes))
		}
		total += n
	}
	if total > lim.BundleBytes {
		return kernel.TooLarge("bundle_too_large", "The bundle is %s. The limit for one bundle is %s. Remove or shrink some assets.", mb(total), mb(lim.BundleBytes))
	}
	return nil
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
