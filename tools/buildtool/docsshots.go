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
const docsImages = "docs/images"

// maxDocsImages caps the pictures of the guide. `just verify` holds the repository to it.
const maxDocsImages = 24 * 1000 * 1000

// docsWidth is the width of every picture of the guide. The app draws at shotWidth, where the
// bundle screen shows its rail beside the doc, and every picture scales down to docsWidth.
const docsWidth = 1200

const shotWidth = 1440

// cmdDocsShots captures the pictures of docs/guide.md from the real app. It serves a copy of
// build/dev-bundles in local mode, drives it with agent-browser, and writes docs/images.
// The review calls a real model through the local claude CLI, with the model in DOCS_MODEL.
func cmdDocsShots() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	bin := filepath.Join(root, "bin", "speccy")
	if _, err := os.Stat(bin); err != nil {
		return errors.New("bin/speccy is missing: run just build first")
	}
	seed := filepath.Join(root, "build", "dev-bundles")
	if _, err := os.Stat(seed); err != nil {
		return errors.New("build/dev-bundles is missing: run just dev once, or copy testdata/bundles there")
	}
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
	// The seed folder is also the dev server's folder, so it can hold a store. A copy of that
	// store's key is readable by others, and the server refuses it; the shots start clean.
	if err := os.RemoveAll(filepath.Join(dir, ".speccy", "state")); err != nil {
		return err
	}
	for _, f := range mustGlob(filepath.Join(dir, "*", "*.golden.json")) {
		_ = os.Remove(f)
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
	if err := d.guide(s, dir, model); err != nil {
		return err
	}
	return d.linked(s, dir, model)
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

func (d *shots) settle() {
	_, _ = d.ab("wait", "--load", "networkidle")
	_, _ = d.ab("wait", "1200")
}

// png opens url, runs prep, and writes docs/images/<name>.png.
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

// gif records act and writes docs/images/<name>.gif. A GIF carries what the prose cannot:
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
	bs, err := s.bundles()
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
	if err := d.png("guide-editor", u("/bundles/"+draft.ID+"?view=split"), nil); err != nil {
		return err
	}
	if err := d.gif("guide-edit-preview", u("/bundles/"+draft.ID+"?view=preview"), 1, func() error {
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
	if err := d.gif("guide-run-review", u("/bundles/"+draft.ID+"?view=preview"), 12, func() error {
		if err := s.call("POST", "/bundles/"+draft.ID+"/runs", nil, &started); err != nil {
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
	if err := d.png("guide-verdict", u("/bundles/"+draft.ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := d.png("guide-findings", u("/bundles/"+draft.ID+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-questions", u("/bundles/"+draft.ID+"?view=preview"), func() error {
		if err := d.tab("Evidence"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	fresh, err := s.bundle(draft.ID)
	if err != nil {
		return err
	}
	if fresh.Verdict == nil {
		return errors.New("the review of draft-prd made no verdict")
	}
	if err := d.png("guide-run-report", u("/bundles/"+draft.ID+"/runs/"+fresh.Verdict.RunID), nil); err != nil {
		return err
	}

	// 4. The tour, point to point.
	if err := d.gif("guide-tour", u("/bundles/"+draft.ID+"/tour"), 1, func() error {
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
	if err := s.call("POST", "/bundles/"+draft.ID+"/threads", map[string]any{
		"anchor_kind":  "section",
		"anchor":       map[string]any{"heading_path": []string{"Loyalty points", "Users"}},
		"addressed_to": "humans",
		"title":        "Who is this for?",
		"body":         "The Users section says TBD. Name the two groups of customers this serves, and what each one does today.",
		"blocking":     true,
	}, &thread); err != nil {
		return err
	}
	if err := d.png("guide-reviewer", u("/bundles/"+draft.ID+"?as=reviewer"), nil); err != nil {
		return err
	}
	if err := d.png("guide-reviewer-questions", u("/bundles/"+draft.ID+"/tour?as=reviewer"), nil); err != nil {
		return err
	}

	// 6. Build Ready, and the packet a builder takes. A builder takes the packet first, so the
	// handoffs of the bundle are real.
	if err := s.call("POST", "/bundles/"+ready.ID+"/handoff", map[string]any{}, nil); err != nil {
		return err
	}
	if err := d.png("guide-build-ready", u("/bundles/"+ready.ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := d.png("guide-handoff", u("/bundles/"+ready.ID+"?view=preview"), func() error {
		if err := d.tab("History"); err != nil {
			return err
		}
		_, err := d.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	if err := d.png("guide-trace", u("/bundles/"+ready.ID+"/trace"), nil); err != nil {
		return err
	}

	return nil
}

// linked captures the pictures of docs/linked-docs.md: two docs that must agree. It uses the
// payments PRD and the SDD that implements it, and the two SDDs that break a rule on purpose.
func (d *shots) linked(s *server, dir, model string) error {
	u := func(p string) string { return s.base + p }
	bs, err := s.bundles()
	if err != nil {
		return err
	}
	for _, slug := range []string{"payments-prd", "payments-sdd", "restated-sdd", "contradicting-sdd"} {
		if _, ok := bs[slug]; !ok {
			return fmt.Errorf("no bundle %s in build/dev-bundles", slug)
		}
	}
	prd, sdd := bs["payments-prd"], bs["payments-sdd"]

	// The matrix: every upstream ID against the docs that implement it.
	if err := d.png("linked-matrix", u("/bundles/"+prd.ID+"/trace"), nil); err != nil {
		return err
	}

	// A coverage gap: the sidecar of the SDD holds the acknowledgement of REQ-003, so taking
	// it away opens the gap that a reader of the doc meets first.
	sidecar := filepath.Join(dir, ".speccy", "decisions", "payments-sdd", "SPEC.md.yaml")
	kept, err := os.ReadFile(sidecar)
	if err != nil {
		return fmt.Errorf("the sidecar of payments-sdd is missing: %w", err)
	}
	if err := os.Remove(sidecar); err != nil {
		return err
	}
	if _, err := d.ab("wait", "5000"); err != nil {
		return err
	}
	if err := d.png("linked-gap", u("/bundles/"+sdd.ID+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		_, err := d.ab("wait", "800")
		return err
	}); err != nil {
		return err
	}
	// The acknowledgement closes it, and the doc itself does not change.
	if err := os.WriteFile(sidecar, kept, 0o644); err != nil {
		return err
	}
	if _, err := d.ab("wait", "5000"); err != nil {
		return err
	}
	if err := d.png("linked-ack", u("/bundles/"+sdd.ID+"/trace"), nil); err != nil {
		return err
	}

	// A restatement: a downstream paragraph that repeats the upstream instead of linking.
	if err := d.png("linked-restatement", u("/bundles/"+bs["restated-sdd"].ID+"?view=preview"), func() error {
		if err := d.tab("Findings"); err != nil {
			return err
		}
		_, err := d.ab("wait", "800")
		return err
	}); err != nil {
		return err
	}

	// A contradiction: the coherence stage reads both docs, so this one calls the model.
	var started struct{ ID string }
	if err := s.call("POST", "/bundles/"+bs["contradicting-sdd"].ID+"/runs", nil, &started); err != nil {
		return err
	}
	if err := s.waitRun(started.ID); err != nil {
		return err
	}
	// The Contradicted overlay layer shows the conflict itself, not every other finding.
	if err := d.png("linked-contradiction", u("/bundles/"+bs["contradicting-sdd"].ID+"?view=preview"), func() error {
		if _, err := d.ab("find", "text", "Contradicted", "click"); err != nil {
			return err
		}
		_, err := d.ab("wait", "1500")
		return err
	}); err != nil {
		return err
	}

	// An upstream edit makes the downstream verdict stale. Only a full verdict goes stale: a
	// lint verdict is cheap, so Speccy lints it again instead.
	var full struct{ ID string }
	if err := s.call("POST", "/bundles/"+sdd.ID+"/runs", nil, &full); err != nil {
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
	if err := d.png("linked-stale", u("/bundles/"+sdd.ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := os.WriteFile(prdDoc, text, 0o644); err != nil {
		return err
	}

	fmt.Printf("docs-shots: %d pictures in %s\n", d.count, d.out)
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
