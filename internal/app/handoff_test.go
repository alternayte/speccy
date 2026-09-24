package app_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
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

// The build packet of a database-held bundle leaves out the sidecar: the waivers are Speccy's
// record, not part of the design, and HANDOFF.md does not list the sidecar as an asset.
func TestHandoff_PacketLeavesOutTheSidecar(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			e.waive(t)
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Fatalf("fixture verdict %s", r)
			}
			res, err := e.app.API.TakeHandoff(as("author"), api.TakeHandoffRequestObject{DocId: e.b.ID, Body: &api.TakeHandoffJSONRequestBody{}})
			if err != nil {
				t.Fatal(err)
			}
			p := api.BuildPacket(res.(api.TakeHandoff200JSONResponse))
			for _, f := range p.Files {
				if source.IsSidecar(f.Path) {
					t.Errorf("the packet holds the sidecar %s", f.Path)
				}
			}
			if strings.Contains(p.HandoffMd, source.SidecarDir) {
				t.Errorf("HANDOFF.md lists the sidecar:\n%s", p.HandoffMd)
			}
		})
	}
}

// The .zip the app downloads holds the files speccy handoff --out writes, in one folder: the
// spec doc, its assets, the linked spec docs under links/, and HANDOFF.md. Taking it records
// the handoff with its label, and a Not Build Ready verdict needs acknowledged.
func TestHandoff_ZipHoldsThePacketAndRecordsTheHandoff(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			ctx := as("author")
			up := "---\ntype: note\ntitle: Upstream\n---\n\n# Upstream\n\n## Scope\n\nThe gateway takes cards.\n"
			if _, err := e.app.Bundles.CreateDB(ctx, "up", []source.File{{Path: "NOTE.md", Content: []byte(up)}}, "author"); err != nil {
				t.Fatal(err)
			}
			png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
			e.verdict(t)
			if _, _, err := e.app.Bundles.Change(ctx, e.b.ID, e.b.CurrentVersionID.UUID,
				source.Op{Kind: source.OpWrite, Path: "limits.png", Content: png}, "author", "Add the limits"); err != nil {
				t.Fatal(err)
			}
			linked := strings.Replace(doc, "title: Pay\n", "title: Pay\nlinks:\n  - kind: references\n    target: up\n", 1)

			// Not Build Ready: the placeholder is a MUST finding.
			e.edit(t, linked)
			if r, _ := e.verdict(t); r != "not_build_ready" {
				t.Fatalf("fixture verdict %s, want not_build_ready", r)
			}
			_, err := e.app.API.TakeHandoffZip(ctx, api.TakeHandoffZipRequestObject{DocId: e.b.ID, Body: &api.HandoffRequest{}})
			if ke, ok := kernel.AsError(err); !ok || ke.Code != "not_build_ready" {
				t.Fatalf("a zip of a Not Build Ready doc: %v, want not_build_ready", err)
			}
			ack := true
			if _, err := e.app.API.TakeHandoffZip(ctx, api.TakeHandoffZipRequestObject{DocId: e.b.ID, Body: &api.HandoffRequest{Acknowledged: &ack}}); err != nil {
				t.Fatal(err)
			}

			e.edit(t, strings.Replace(linked, "is TBD for now", "is 100 requests a second", 1))
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Fatalf("fixture verdict %s, want build_ready", r)
			}
			label := "acme/pay"
			res, err := e.app.API.TakeHandoffZip(ctx, api.TakeHandoffZipRequestObject{DocId: e.b.ID, Body: &api.HandoffRequest{Label: &label}})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			if err := res.VisitTakeHandoffZipResponse(w); err != nil {
				t.Fatal(err)
			}

			list, err := e.app.API.ListHandoffs(ctx, api.ListHandoffsRequestObject{DocId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			items := list.(api.ListHandoffs200JSONResponse).Items
			if len(items) != 2 {
				t.Fatalf("%d handoffs, want 2", len(items))
			}
			h, spike := items[0], items[1]
			if h.Label == nil || *h.Label != label || h.Verdict != "build_ready" || h.Acknowledged {
				t.Errorf("the zip handoff reads label %v, verdict %s, acknowledged %v", h.Label, h.Verdict, h.Acknowledged)
			}
			if spike.Verdict != "not_build_ready" || !spike.Acknowledged {
				t.Errorf("the acknowledged handoff reads verdict %s, acknowledged %v", spike.Verdict, spike.Acknowledged)
			}

			folder := fmt.Sprintf("pay-v%d-build-packet", h.VersionNumber)
			if got, want := w.Header().Get("Content-Disposition"), fmt.Sprintf("attachment; filename=%q", folder+".zip"); got != want {
				t.Errorf("Content-Disposition %q, want %q", got, want)
			}
			zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{}
			var names []string
			for _, f := range zr.File {
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, f.Name)
				got[f.Name] = body
			}
			want := []string{folder + "/NOTE.md", folder + "/limits.png", folder + "/links/up.md", folder + "/HANDOFF.md"}
			sort.Strings(names)
			sort.Strings(want)
			if !slices.Equal(names, want) {
				t.Fatalf("the zip holds %v, want %v", names, want)
			}
			if !bytes.Equal(got[folder+"/limits.png"], png) {
				t.Errorf("the asset is %v, want the bytes the bundle holds", got[folder+"/limits.png"])
			}
			if string(got[folder+"/links/up.md"]) != up {
				t.Errorf("links/up.md is not the linked spec doc:\n%s", got[folder+"/links/up.md"])
			}
			if !strings.Contains(string(got[folder+"/HANDOFF.md"]), h.Id.String()) {
				t.Errorf("HANDOFF.md does not quote the recorded handoff %s", h.Id)
			}
		})
	}
}
