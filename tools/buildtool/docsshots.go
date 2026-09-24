package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// docsImages holds every picture of the guide. Only cmdDocsShots writes here, so no picture
// of the guide can drift away from the app.
const docsImages = "site/src/assets/shots"

// maxDocsImages caps the pictures of the guide. `just verify` holds the repository to it.
const maxDocsImages = 24 * 1000 * 1000

// docsWidth is the width of every picture of the guide. The app draws at shotWidth, where the
// bundle screen shows its rail beside the doc, and every picture scales down to docsWidth.
const docsWidth = 1200

const shotWidth = 1440

// cmdDocsShots captures the pictures of the docs site from the real app, in the dark theme.
// It serves a copy of the testdata/bundles fixtures in local mode, so each run starts from
// the same docs, drives it with agent-browser, and writes site/src/assets/shots. The review calls a real model through the local claude CLI, with the
// model in DOCS_MODEL. part "linked" captures only the pictures of the tutorial Link an SDD to
// a PRD, and part "howto" those and the how-to pictures that build on its docs.
func cmdDocsShots(part string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	bin := filepath.Join(root, "bin", "speccy")
	if _, err := os.Stat(bin); err != nil {
		return errors.New("bin/speccy is missing: run just build first")
	}
	seed := filepath.Join(root, "testdata", "bundles")
	out := filepath.Join(root, docsImages)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "speccy-docs-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	dir := filepath.Join(work, "bundles")
	if err := os.CopyFS(dir, os.DirFS(seed)); err != nil {
		return err
	}
	for _, f := range mustGlob(filepath.Join(dir, "*", "*.golden.json")) {
		_ = os.Remove(f)
	}
	if err := os.WriteFile(filepath.Join(dir, ".speccy.yaml"), []byte(linkPatterns), 0o644); err != nil {
		return err
	}
	model := os.Getenv("DOCS_MODEL")
	if model == "" {
		model = "haiku"
	}

	s, err := start(bin, dir, nil, "serve", "--dir", dir)
	if err != nil {
		return err
	}
	defer s.stop()
	d := &shots{out: out, session: "speccy-docs"}
	defer func() { _, _ = d.ab("close") }()
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "900"); err != nil {
		return err
	}
	if err := d.dark(); err != nil {
		return err
	}
	defer func() { fmt.Printf("docs-shots: %d pictures in %s\n", d.count, d.out) }()
	if part == "linked" || part == "howto" {
		if err := s.models(model); err != nil {
			return err
		}
		if err := d.linked(s, dir); err != nil || part == "linked" {
			return err
		}
		return d.howto(s, dir, work)
	}
	if err := d.guide(s, dir, model); err != nil {
		return err
	}
	if err := d.linked(s, dir); err != nil {
		return err
	}
	return d.howto(s, dir, work)
}

// models points every role at the local claude CLI, with model.
func (s *server) models(model string) error {
	var backend struct{ ID string }
	if err := s.call("POST", "/admin/backends", map[string]string{"kind": "agent_cli", "name": "claude", "preset": "claude"}, &backend); err != nil {
		return err
	}
	for _, role := range []string{"reviewer", "reader_1", "reader_2", "reader_3", "judge", "writer"} {
		if err := s.call("PUT", "/admin/roles/"+role, map[string]string{"backend_id": backend.ID, "model": model}, nil); err != nil {
			return err
		}
	}
	return nil
}

// specDoc is one spec doc, as the list of bundles names it. A bundle is a folder; the
// screens, the reviews and the tour belong to one spec doc in it.
type specDoc struct {
	ID       string `json:"id"`
	BundleID string `json:"bundle_id"`
	Slug     string `json:"slug"`
	Verdict  *struct {
		RunID  string `json:"run_id"`
		Result string `json:"result"`
	} `json:"verdict"`
	Version struct {
		ID string `json:"id"`
	} `json:"current_version"`
}

// page is the doc's page in its bundle.
func (d specDoc) page() string { return "/bundles/" + d.BundleID + "/docs/" + d.ID }

// docs returns every spec doc, by slug.
func (s *server) docs() (map[string]specDoc, error) {
	var l struct {
		Items []struct{ Docs []specDoc }
	}
	if err := s.call("GET", "/bundles?limit=100", nil, &l); err != nil {
		return nil, err
	}
	m := map[string]specDoc{}
	for _, b := range l.Items {
		for _, d := range b.Docs {
			m[d.Slug] = d
		}
	}
	return m, nil
}

// doc returns one spec doc as it is now.
func (s *server) doc(id string) (specDoc, error) {
	var d specDoc
	err := s.call("GET", "/docs/"+id, nil, &d)
	return d, err
}

// finding returns the finding of the doc's latest verdict with slug whose message holds text.
func (s *server) finding(id, slug, text string) (run, finding string, err error) {
	d, err := s.doc(id)
	if err != nil {
		return "", "", err
	}
	if d.Verdict == nil {
		return "", "", fmt.Errorf("doc %s has no verdict", d.Slug)
	}
	var l struct {
		Items []struct {
			ID        string `json:"id"`
			RunID     string `json:"run_id"`
			CheckSlug string `json:"check_slug"`
			Message   string `json:"message"`
		}
	}
	if err := s.call("GET", "/runs/"+d.Verdict.RunID+"/findings", nil, &l); err != nil {
		return "", "", err
	}
	for _, f := range l.Items {
		if f.CheckSlug == slug && strings.Contains(f.Message, text) {
			return f.RunID, f.ID, nil
		}
	}
	return "", "", fmt.Errorf("doc %s has no %s finding about %q", d.Slug, slug, text)
}

type shots struct {
	out     string
	session string
	count   int
}

func (d *shots) ab(args ...string) (string, error) {
	cmd := exec.Command("agent-browser", args...)
	cmd.Env = append(os.Environ(), "AGENT_BROWSER_SESSION="+d.session)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("agent-browser %s: %w: %s", strings.Join(args, " "), err, b)
	}
	return string(b), nil
}

// dark captures the dark theme only: the docs site shows the app as most people run it. The
// app follows the colour scheme when it has no stored theme, and so does GitHub.
func (d *shots) dark() error {
	_, err := d.ab("set", "media", "dark")
	return err
}

func (d *shots) settle() {
	_, _ = d.ab("wait", "--load", "networkidle")
	_, _ = d.ab("wait", "1200")
}

// png opens url, runs prep, and writes site/src/assets/shots/<name>.png.
func (d *shots) png(name, url string, prep func() error) error {
	if _, err := d.ab("open", url); err != nil {
		return err
	}
	d.settle()
	if prep != nil {
		if err := prep(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	file := filepath.Join(d.out, name+".png")
	if _, err := d.ab("screenshot", file); err != nil {
		return err
	}
	if err := scaleDown(file); err != nil {
		return err
	}
	d.count++
	fmt.Println("docs-shots:", name+".png")
	return nil
}

// gif records act and writes site/src/assets/shots/<name>.gif. A GIF carries what the prose cannot:
// the review running, the click-to-edit, and the tour moving point to point.
func (d *shots) gif(name, url string, speed int, act func() error) error {
	if _, err := d.ab("open", url); err != nil {
		return err
	}
	d.settle()
	webm := filepath.Join(os.TempDir(), name+".webm")
	_ = os.Remove(webm)
	if _, err := d.ab("record", "start", webm, "--fps", "10"); err != nil {
		return err
	}
	actErr := act()
	if _, err := d.ab("record", "stop"); err != nil {
		return err
	}
	if actErr != nil {
		return fmt.Errorf("%s: %w", name, actErr)
	}
	gif := filepath.Join(d.out, name+".gif")
	filter := fmt.Sprintf("setpts=PTS/%d,fps=10,scale=%d:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=64[p];[b][p]paletteuse=dither=bayer", speed, docsWidth)
	cmd := exec.Command("ffmpeg", "-y", "-i", webm, "-vf", filter, "-loop", "0", gif)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", name, err, b)
	}
	d.count++
	fmt.Println("docs-shots:", name+".gif")
	return nil
}

// tab clicks one tab of the review rail by its label. A tab appears only when the doc's state
// earns it, so its place moves; and its name carries a count, so a name does not select it.
func (d *shots) tab(label string) error {
	js := fmt.Sprintf(`(() => { const b=[...document.querySelectorAll("[role=tab]")].find(x=>x.textContent.trim().startsWith(%q)); if(!b) throw new Error("no tab "+%q); b.click(); return true; })()`, label, label)
	_, err := d.ab("eval", js)
	return err
}

// scaleDown rewrites a picture at docsWidth.
func scaleDown(file string) error {
	small := file + ".tmp.png"
	cmd := exec.Command("ffmpeg", "-y", "-i", file, "-vf", fmt.Sprintf("scale=%d:-1:flags=lanczos", docsWidth), small)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", file, err, b)
	}
	return os.Rename(small, file)
}

// guide captures the pictures of the guide in the order the guide tells the story.
func (d *shots) guide(s *server, dir, model string) error {
	u := func(p string) string { return s.base + p }
	bs, err := s.docs()
	if err != nil {
		return err
	}
	for _, slug := range []string{"draft-prd", "payments-prd", "audit-sdd"} {
		if _, ok := bs[slug]; !ok {
			return fmt.Errorf("no bundle %s in build/dev-bundles", slug)
		}
	}
	draft, ready := bs["draft-prd"], bs["audit-sdd"]

	// 1. The list, and the dialog that starts a bundle.
	if err := d.png("guide-bundles", u("/"), nil); err != nil {
		return err
	}
	if err := d.png("guide-new-bundle", u("/"), func() error {
		if _, err := d.ab("find", "role", "button", "click", "--name", "New"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}

	// The way in for a doc Speccy did not write: the import dialog with its guess, and the
	// files the scan passed over. The loose doc lands after the first list picture.
	loose := filepath.Join(dir, "payment-retries.md")
	body := "# Payment retries\n\n## Problem\n\nA failed card payment ends the order.\n\n## Goals\n\n" +
		"- G-1: Fewer orders lost to one failed charge.\n\n## Requirements\n\n- REQ-001: Speccy retries a failed charge twice.\n"
	if err := os.WriteFile(loose, []byte(body), 0o644); err != nil {
		return err
	}
	if _, err := d.ab("wait", "3000"); err != nil {
		return err
	}
	if err := d.png("guide-import", u("/"), func() error {
		if _, err := d.ab("find", "role", "button", "click", "--name", "Import"); err != nil {
			return err
		}
		if _, err := d.ab("wait", "600"); err != nil {
			return err
		}
		if _, err := d.ab("upload", "#import-file", loose); err != nil {
			return err
		}
		_, err := d.ab("wait", "1500")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-adopt", u("/"), nil); err != nil {
		return err
	}

	// 2. Writing the doc: the split view, and the click-to-edit in the preview.
	if err := d.png("guide-editor", u(draft.page()+"?view=split"), nil); err != nil {
		return err
	}
	if err := d.gif("guide-edit-preview", u(draft.page()+"?view=preview"), 1, func() error {
		if _, err := d.ab("find", "first", "article p", "click"); err != nil {
			return err
		}
		if _, err := d.ab("wait", "700"); err != nil {
			return err
		}
		if _, err := d.ab("keyboard", "type", " The team ships this first."); err != nil {
			return err
		}
		_, err := d.ab("wait", "1500")
		return err
	}); err != nil {
		return err
	}

	// 3. The review: the models, the run to a verdict, the verdict and its findings.
	var backend struct{ ID string }
	if err := s.call("POST", "/admin/backends", map[string]string{"kind": "agent_cli", "name": "claude", "preset": "claude"}, &backend); err != nil {
		return err
	}
	for _, role := range []string{"reviewer", "reader_1", "reader_2", "reader_3", "judge", "writer"} {
		if err := s.call("PUT", "/admin/roles/"+role, map[string]string{"backend_id": backend.ID, "model": model}, nil); err != nil {
			return err
		}
	}
	if err := d.png("guide-models", u("/admin"), nil); err != nil {
		return err
	}
	var started struct{ ID string }
	if err := d.gif("guide-run-review", u(draft.page()+"?view=preview"), 12, func() error {
		if err := s.call("POST", "/docs/"+draft.ID+"/runs", nil, &started); err != nil {
			return err
		}
		if err := s.waitRun(started.ID); err != nil {
			return err
		}
		_, err := d.ab("wait", "3000")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-verdict", u(draft.page()+"?view=preview"), nil); err != nil {
		return err
	}
	if err := d.png("guide-findings", u(draft.page()+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-questions", u(draft.page()+"?view=preview"), func() error {
		if err := d.tab("Evidence"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	fresh, err := s.doc(draft.ID)
	if err != nil {
		return err
	}
	if fresh.Verdict == nil {
		return errors.New("the review of draft-prd made no verdict")
	}
	if err := d.png("guide-run-report", u(draft.page()+"/runs/"+fresh.Verdict.RunID), nil); err != nil {
		return err
	}

	// 4. The tour, point to point.
	if err := d.gif("guide-tour", u(draft.page()+"/tour"), 1, func() error {
		for i := 0; i < 3; i++ {
			if _, err := d.ab("press", "j"); err != nil {
				return err
			}
			if _, err := d.ab("wait", "1400"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// 5. Reviewer mode: what a person who cannot edit the bundle sees. A blocking thread gives
	// the reviewer's questions screen a real point to answer.
	var thread struct{ ID string }
	if err := s.call("POST", "/docs/"+draft.ID+"/threads", map[string]any{
		"anchor_kind":  "section",
		"anchor":       map[string]any{"heading_path": []string{"Loyalty points", "Users"}},
		"addressed_to": "humans",
		"title":        "Who is this for?",
		"body":         "The Users section says TBD. Name the two groups of customers this serves, and what each one does today.",
		"blocking":     true,
	}, &thread); err != nil {
		return err
	}
	if err := d.png("guide-reviewer", u(draft.page()+"?as=reviewer"), nil); err != nil {
		return err
	}
	if err := d.png("guide-reviewer-questions", u(draft.page()+"/tour?as=reviewer"), nil); err != nil {
		return err
	}

	// 6. Build Ready, and the packet a builder takes. A builder takes the packet first, so the
	// handoffs of the bundle are real.
	if err := s.call("POST", "/docs/"+ready.ID+"/handoff", map[string]any{}, nil); err != nil {
		return err
	}
	if err := d.png("guide-build-ready", u(ready.page()+"?view=preview"), nil); err != nil {
		return err
	}
	if err := d.png("guide-handoff", u(ready.page()+"?view=preview"), func() error {
		if err := d.tab("History"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-trace", u(ready.page()+"/trace"), nil); err != nil {
		return err
	}

	return nil
}

// linkedPRD and linkedSDD are the two docs of the tutorial Link an SDD to a PRD, in one folder. The PRD's
// requirements have no IDs yet, and the SDD has no link yet: the pictures follow the steps
// that add both.
const linkedPRD = `---
type: prd
---
# PRD - Refunds

## Problem

Support staff refund orders by hand, and each refund takes a day.

## Users

Support staff, and the customers they refund.

## Goals

- Refund a paid order in one click.

## Non-goals

- No partial refunds.

## Requirements

- Support staff can refund a paid order from the order page.
- The customer gets an email when the refund starts.
- The refund reaches the customer within 5 working days.
`

const linkedSDD = `---
type: sdd
---
# SDD - Refunds

## Context

Support staff refund orders by hand today.

## Design

The order page gets a Refund button. The service calls the payment provider's refund API and records the refund.

## Decisions

- DEC-001: We use the provider's refund API, not a manual bank transfer.

## Non-goals

No partial refunds.
`

// linked captures the pictures of the tutorial Link an SDD to a PRD in the order of its steps: one folder
// with a PRD and an SDD, the link, the IDs, the matrix, the three answers to a gap, then the
// restatement, the contradiction and the stale verdict of the payments fixtures.
func (d *shots) linked(s *server, dir string) error {
	u := func(p string) string { return s.base + p }
	if err := os.MkdirAll(filepath.Join(dir, "refunds"), 0o755); err != nil {
		return err
	}
	for name, body := range map[string]string{"PRD - Refunds.md": linkedPRD, "SDD - Refunds.md": linkedSDD} {
		if err := os.WriteFile(filepath.Join(dir, "refunds", name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if _, err := d.ab("wait", "5000"); err != nil {
		return err
	}
	ds, err := s.docs()
	if err != nil {
		return err
	}
	for _, slug := range []string{"refunds/PRD - Refunds", "refunds/SDD - Refunds", "payments-prd", "payments-sdd", "restated-sdd", "contradicting-sdd"} {
		if _, ok := ds[slug]; !ok {
			return fmt.Errorf("no spec doc %s", slug)
		}
	}
	prd, sdd := ds["refunds/PRD - Refunds"], ds["refunds/SDD - Refunds"]

	// 1. One folder, one bundle, two spec docs.
	if err := d.png("linked-folder", u(sdd.page()+"?view=preview"), nil); err != nil {
		return err
	}

	// 2. The link: Suggest fix on links.has-upstream lists the PRDs.
	if err := d.png("linked-link", u(sdd.page()+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		js := `(() => { const li=[...document.querySelectorAll("li")].find(x=>x.textContent.includes("links.has-upstream")); if(!li) throw new Error("no has-upstream finding"); const b=[...li.querySelectorAll("button")].find(x=>x.textContent.trim()==="Suggest fix"); b.click(); return true; })()`
		if _, err := d.ab("eval", js); err != nil {
			return err
		}
		_, err := d.ab("wait", "1500")
		return err
	}); err != nil {
		return err
	}
	run, up, err := s.finding(sdd.ID, "links.has-upstream", "")
	if err != nil {
		return err
	}
	if err := s.call("POST", "/runs/"+run+"/findings/"+up+"/fix", nil, nil); err != nil {
		return err
	}
	if err := s.call("POST", "/runs/"+run+"/findings/"+up+"/fix/accept", map[string]string{"link_to": prd.ID}, nil); err != nil {
		return err
	}

	// 3. The IDs: the PRD's Traceability page suggests them, and Add IDs writes them in.
	if err := d.png("linked-ids", u(prd.page()+"/trace"), nil); err != nil {
		return err
	}
	if _, err := d.ab("find", "role", "button", "click", "--name", "Add 3 IDs"); err != nil {
		return err
	}
	if _, err := d.ab("wait", "3000"); err != nil {
		return err
	}

	// 4. The matrix: three PRD IDs, three gaps.
	if err := d.png("linked-matrix", u(prd.page()+"/trace"), nil); err != nil {
		return err
	}

	// 5. The three answers to a gap, in the tour.
	var trace struct{ Sections [][]string }
	if err := s.call("GET", "/docs/"+sdd.ID+"/trace", nil, &trace); err != nil {
		return err
	}
	design := -1
	for i, p := range trace.Sections {
		if len(p) > 0 && p[len(p)-1] == "Design" {
			design = i
		}
	}
	if design < 0 {
		return errors.New("the SDD has no Design section")
	}
	if err := d.png("linked-gap", u(sdd.page()+"/tour"), func() error {
		if _, err := d.ab("press", "d"); err != nil {
			return err
		}
		if _, err := d.ab("wait", "500"); err != nil {
			return err
		}
		if _, err := d.ab("find", "role", "radio", "click", "--name", "This doc covers it"); err != nil {
			return err
		}
		if _, err := d.ab("wait", "800"); err != nil {
			return err
		}
		if _, err := d.ab("select", "aside select, select", fmt.Sprint(design)); err != nil {
			return err
		}
		_, err := d.ab("wait", "500")
		return err
	}); err != nil {
		return err
	}
	now, err := s.doc(sdd.ID)
	if err != nil {
		return err
	}
	if err := s.call("POST", "/docs/"+sdd.ID+"/trace/cover?base_version="+now.Version.ID,
		map[string]any{"trace_id": "REQ-001", "section": trace.Sections[design]}, nil); err != nil {
		return err
	}
	if _, err := d.ab("wait", "2000"); err != nil {
		return err
	}
	for _, a := range []struct{ id, status, target, reason string }{
		{"REQ-002", "out_of_scope", "", "The mail service sends every customer email, not this service."},
		{"REQ-003", "covered_by", "payments-sdd", "The payments service owns the provider's refund timing."},
	} {
		_, f, err := s.finding(sdd.ID, "trace.coverage", a.id+" ")
		if err != nil {
			return err
		}
		answer := map[string]string{"status": a.status}
		if a.target != "" {
			answer["target"] = a.target
		}
		var w struct{ ID string }
		if err := s.call("POST", "/docs/"+sdd.ID+"/waivers", map[string]any{"finding_id": f, "reason": a.reason, "trace": answer}, &w); err != nil {
			return err
		}
		if err := s.call("POST", "/waivers/"+w.ID+"/approve", nil, nil); err != nil {
			return err
		}
	}
	if _, err := d.ab("wait", "3000"); err != nil {
		return err
	}
	if err := d.png("linked-ack", u(prd.page()+"/trace"), nil); err != nil {
		return err
	}

	// 6. A restatement: a downstream paragraph that repeats the upstream instead of linking.
	if err := d.png("linked-restatement", u(ds["restated-sdd"].page()+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		_, err := d.ab("wait", "800")
		return err
	}); err != nil {
		return err
	}

	// 7. A contradiction: the coherence stage reads both docs, so this one calls the model.
	var started struct{ ID string }
	if err := s.call("POST", "/docs/"+ds["contradicting-sdd"].ID+"/runs", nil, &started); err != nil {
		return err
	}
	if err := s.waitRun(started.ID); err != nil {
		return err
	}
	// The Contradicted overlay layer shows the conflict itself, not every other finding.
	if err := d.png("linked-contradiction", u(ds["contradicting-sdd"].page()+"?view=preview"), func() error {
		if _, err := d.ab("find", "text", "Contradicted", "click"); err != nil {
			return err
		}
		_, err := d.ab("wait", "1500")
		return err
	}); err != nil {
		return err
	}

	// 8. An upstream edit makes the downstream verdict stale. Only a full verdict goes stale: a
	// lint verdict is cheap, so Speccy lints it again instead.
	var full struct{ ID string }
	if err := s.call("POST", "/docs/"+ds["payments-sdd"].ID+"/runs", nil, &full); err != nil {
		return err
	}
	if err := s.waitRun(full.ID); err != nil {
		return err
	}
	prdDoc := filepath.Join(dir, "payments-prd", "PRD.md")
	text, err := os.ReadFile(prdDoc)
	if err != nil {
		return err
	}
	next := strings.Replace(string(text), "HTTP 503", "HTTP 503 or an HTTP 429", 1)
	if next == string(text) {
		return errors.New("payments-prd/PRD.md no longer holds the requirement the picture edits")
	}
	if err := os.WriteFile(prdDoc, []byte(next), 0o644); err != nil {
		return err
	}
	if _, err := d.ab("wait", "6000"); err != nil {
		return err
	}
	if err := d.png("linked-stale", u(ds["payments-sdd"].page()+"?view=preview"), nil); err != nil {
		return err
	}
	if err := os.WriteFile(prdDoc, text, 0o644); err != nil {
		return err
	}
	return nil
}

func mustGlob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

// docsImagesSize is the total size of the pictures of the guide.
func docsImagesSize() (int64, error) {
	var total int64
	err := filepath.Walk(docsImages, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}
