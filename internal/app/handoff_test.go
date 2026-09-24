package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// A handoff refused by a blocking thread alone names the thread, not "0 MUST findings", and
// says how to take the packet anyway from the CLI and from the API.
func TestHandoff_RefusalNamesTheBlockingThread(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			fixed(e, t)
			blocking := true
			if _, err := e.app.API.OpenBundleThread(as("member"), api.OpenBundleThreadRequestObject{DocId: e.b.ID, Body: &api.OpenThread{
				AnchorKind: api.OpenThreadAnchorKindSection, Anchor: map[string]any{"heading_path": []string{"Pay", "Limits"}},
				AddressedTo: api.OpenThreadAddressedToHumans, Body: "Who owns the limit?", Blocking: &blocking}}); err != nil {
				t.Fatal(err)
			}
			if r, must := e.verdict(t); r != "not_build_ready" || must != 0 {
				t.Fatalf("fixture verdict %s with %d MUST, want not_build_ready with 0", r, must)
			}
			_, err := e.app.API.TakeHandoff(as("author"), api.TakeHandoffRequestObject{DocId: e.b.ID, Body: &api.TakeHandoffJSONRequestBody{}})
			var ke *kernel.Error
			if !errors.As(err, &ke) || ke.Code != "not_build_ready" {
				t.Fatalf("handoff error %v, want not_build_ready", err)
			}
			if strings.Contains(ke.Detail, "0 MUST") || !strings.Contains(ke.Detail, "1 open blocking thread") {
				t.Errorf("the refusal does not name the blocking thread: %s", ke.Detail)
			}
			if !strings.Contains(ke.Detail, "--acknowledged") || !strings.Contains(ke.Detail, "acknowledged: true") {
				t.Errorf("the refusal does not say how to take the packet anyway: %s", ke.Detail)
			}
		})
	}
}
