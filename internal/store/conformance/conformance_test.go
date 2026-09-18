package conformance

import (
	"testing"

	"github.com/alternayte/speccy/internal/store/storetest"
)

// T-020
func TestStoreConformance(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) { Run(t, e.Open) })
	}
}
