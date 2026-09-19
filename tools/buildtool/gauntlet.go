package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// cmdGauntlet captures the screens and states of BUILD.md §6.2 in light and dark themes, at
// 1440 px and 390 px wide, into docs/gauntlet/<run>/. It runs bin/speccy twice: local mode over
// a copy of testdata/bundles, and hosted mode on a fresh database in the compose Postgres for
// the guest view and the invites. The full reviews and the diff summary call a real model: the
// local claude CLI, with the model in GAUNTLET_MODEL (default haiku).
func cmdGauntlet(run string) error {
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(run) {
		return fmt.Errorf("the run name %q must be lower-case letters, digits, and dashes", run)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	bin := filepath.Join(root, "bin", "speccy")
	if _, err := os.Stat(bin); err != nil {
		return errors.New("bin/speccy is missing: run just build first")
	}
	out := filepath.Join(root, "docs", "gauntlet", run)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "speccy-gauntlet-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	model := os.Getenv("GAUNTLET_MODEL")
	if model == "" {
		model = "haiku"
	}
	g := &gauntlet{out: out, session: "gauntlet-" + run}
	defer func() { _, _ = g.ab("close") }()

	if err := g.local(bin, filepath.Join(root, "testdata", "bundles"), filepath.Join(work, "local"), model); err != nil {
		return fmt.Errorf("local mode: %w", err)
	}
	if err := g.hosted(root, bin, filepath.Join(root, "testdata", "bundles", "draft-prd", "PRD.md")); err != nil {
		return fmt.Errorf("hosted mode: %w", err)
	}
	fmt.Printf("gauntlet: %d screenshots in %s\n", g.count, out)
	return nil
}

type gauntlet struct {
	out     string
	session string
	count   int
}

// ab runs one agent-browser command in the gauntlet's own browser session.
func (g *gauntlet) ab(args ...string) (string, error) {
	cmd := exec.Command("agent-browser", args...)
	cmd.Env = append(os.Environ(), "AGENT_BROWSER_SESSION="+g.session)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("agent-browser %s: %w: %s", strings.Join(args, " "), err, b)
	}
	return string(b), nil
}

// shot captures url in both themes and both widths. prep runs after each page load, for
// example to press keys or to wait for text.
func (g *gauntlet) shot(name, url string, prep func() error) error {
	for _, theme := range []string{"light", "dark"} {
		for _, width := range []string{"1440", "390"} {
			height := "900"
			if width == "390" {
				height = "844"
			}
			if _, err := g.ab("set", "viewport", width, height); err != nil {
				return err
			}
			if _, err := g.ab("open", url); err != nil {
				return err
			}
			if _, err := g.ab("eval", fmt.Sprintf("localStorage.setItem('speccy.theme','%s')", theme)); err != nil {
				return err
			}
			if _, err := g.ab("reload"); err != nil {
				return err
			}
			g.settle()
			if prep != nil {
				if err := prep(); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			file := filepath.Join(g.out, fmt.Sprintf("%s-%s-%s.png", name, theme, width))
			if _, err := g.ab("screenshot", file); err != nil {
				return err
			}
			g.count++
		}
	}
	fmt.Println("gauntlet:", name)
	return nil
}

func (g *gauntlet) settle() {
	_, _ = g.ab("wait", "--load", "networkidle")
	_, _ = g.ab("wait", "1200")
}

// server is a running bin/speccy.
type server struct {
	base string
	cmd  *exec.Cmd
	// cookie is the session of the signed-in admin in hosted mode.
	cookie string
}

func start(bin, dir string, env []string, args ...string) (*server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	s := &server{base: "http://" + addr}
	s.cmd = exec.Command(bin, args...)
	s.cmd.Dir = dir
	s.cmd.Env = append(append(os.Environ(), env...), "SPECCY_LISTEN="+addr, "SPECCY_BASE_URL="+s.base)
	if !contains(args, "--hosted") {
		s.cmd.Args = append(s.cmd.Args, "--addr", addr)
	}
	s.cmd.Stdout, s.cmd.Stderr = io.Discard, os.Stderr
	if err := s.cmd.Start(); err != nil {
		return nil, err
	}
	for i := 0; i < 100; i++ {
		if res, err := http.Get(s.base + "/healthz"); err == nil {
			_ = res.Body.Close()
			return s, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.stop()
	return nil, errors.New("the server did not start")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *server) stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}

// call sends a JSON request to the API and decodes the answer into out.
func (s *server) call(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+"/api/v1"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type bundleRef struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Verdict *struct {
		RunID  string `json:"run_id"`
		Result string `json:"result"`
	} `json:"verdict"`
	Version struct {
		ID string `json:"id"`
	} `json:"current_version"`
}

func (s *server) bundles() (map[string]bundleRef, error) {
	var l struct{ Items []bundleRef }
	if err := s.call("GET", "/bundles", nil, &l); err != nil {
		return nil, err
	}
	m := map[string]bundleRef{}
	for _, b := range l.Items {
		m[b.Slug] = b
	}
	return m, nil
}

func (s *server) bundle(id string) (bundleRef, error) {
	var b bundleRef
	err := s.call("GET", "/bundles/"+id, nil, &b)
	return b, err
}

// review runs a full review and waits for it to end.
func (s *server) review(id string) (string, error) {
	var r struct{ ID string }
	if err := s.call("POST", "/bundles/"+id+"/runs", nil, &r); err != nil {
		return "", err
	}
	return r.ID, s.waitRun(r.ID)
}

func (s *server) waitRun(id string) error {
	for deadline := time.Now().Add(15 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		var r struct{ Status, Error string }
		if err := s.call("GET", "/runs/"+id, nil, &r); err != nil {
			return err
		}
		switch r.Status {
		case "complete":
			return nil
		case "failed":
			return errors.New(r.Error)
		}
	}
	return errors.New("the review did not end in 15 minutes")
}

// waitVersion waits until the bundle has a current version other than old: the file watcher
// saw an edit on disk.
func (s *server) waitVersion(id, old string) error {
	for i := 0; i < 100; i++ {
		b, err := s.bundle(id)
		if err != nil {
			return err
		}
		if b.Version.ID != old && b.Verdict != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the edit on disk made no new version")
}

func (g *gauntlet) local(bin, fixtures, dir, model string) error {
	if err := os.CopyFS(dir, os.DirFS(fixtures)); err != nil {
		return err
	}
	golden, _ := filepath.Glob(filepath.Join(dir, "*.golden.json"))
	for _, f := range golden {
		_ = os.Remove(f)
	}
	// A PRD with one SHOULD finding, for the "Build Ready with waivers" state.
	prd, err := os.ReadFile(filepath.Join(dir, "payments-prd", "PRD.md"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "waived-prd"), 0o755); err != nil {
		return err
	}
	waived := strings.Replace(strings.Replace(string(prd), "title: ", "title: Waived ", 1), "\n## ", "\nThe team supports various payment methods.\n\n## ", 2)
	if err := os.WriteFile(filepath.Join(dir, "waived-prd", "PRD.md"), []byte(waived), 0o644); err != nil {
		return err
	}

	s, err := start(bin, dir, nil, "serve", "--dir", dir)
	if err != nil {
		return err
	}
	defer s.stop()
	bs, err := s.bundles()
	if err != nil {
		return err
	}
	for _, slug := range []string{"draft-prd", "payments-prd", "payments-sdd", "waived-prd"} {
		if _, ok := bs[slug]; !ok {
			return fmt.Errorf("no bundle %s in the fixtures", slug)
		}
	}
	u := func(p string) string { return s.base + p }

	// The bundles list: empty, error, and loading (BUILD.md §6.2). The page's fetch answers the
	// list request itself, then the app moves to the list.
	if err := g.shot("bundles-empty", u("/inbox"), func() error {
		if _, err := g.ab("eval", `(() => { const f = window.fetch; window.fetch = (i, o) => String(i.url ?? i).includes("/api/v1/bundles?") ? Promise.resolve(new Response('{"items":[],"problems":[]}', { headers: { "Content-Type": "application/json" } })) : f(i, o); })()`); err != nil {
			return err
		}
		if _, err := g.ab("pushstate", "/"); err != nil {
			return err
		}
		_, err := g.ab("wait", "800")
		return err
	}); err != nil {
		return err
	}
	if err := g.shot("bundles-error", u("/"), func() error {
		if _, err := g.ab("network", "route", "**/api/v1/bundles?limit=*", "--abort"); err != nil {
			return err
		}
		if _, err := g.ab("reload"); err != nil {
			return err
		}
		// The client retries a failed request three times before it shows the error.
		_, err := g.ab("wait", "9000")
		return err
	}); err != nil {
		return err
	}
	_, _ = g.ab("network", "unroute")
	if err := g.shot("bundles-loading", u("/inbox"), func() error {
		// Hold every bundles request, then move to the list inside the app.
		if _, err := g.ab("eval", `(() => { const f = window.fetch; window.fetch = (i, o) => String(i.url ?? i).includes("/api/v1/bundles") ? new Promise(() => {}) : f(i, o); })()`); err != nil {
			return err
		}
		if _, err := g.ab("pushstate", "/"); err != nil {
			return err
		}
		_, err := g.ab("wait", "600")
		return err
	}); err != nil {
		return err
	}
	if err := g.shot("inbox-empty", u("/inbox"), nil); err != nil {
		return err
	}
	if err := g.shot("bundles-list", u("/"), nil); err != nil {
		return err
	}

	// Models: the local claude CLI in every role.
	var backend struct{ ID string }
	if err := s.call("POST", "/admin/backends", map[string]string{"kind": "agent_cli", "name": "claude", "preset": "claude"}, &backend); err != nil {
		return err
	}
	for _, role := range []string{"reviewer", "reader_1", "reader_2", "reader_3", "judge", "writer"} {
		if err := s.call("PUT", "/admin/roles/"+role, map[string]string{"backend_id": backend.ID, "model": model}, nil); err != nil {
			return err
		}
	}
	if err := g.shot("admin-backends", u("/admin"), nil); err != nil {
		return err
	}

	// Verdict bar: running, then a full review of the draft PRD for the overlay and the tour.
	draft := bs["draft-prd"]
	var started struct{ ID string }
	if err := s.call("POST", "/bundles/"+draft.ID+"/runs", nil, &started); err != nil {
		return err
	}
	if err := g.shot("verdict-running", u("/bundles/"+draft.ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := s.waitRun(started.ID); err != nil {
		return fmt.Errorf("full review of draft-prd: %w", err)
	}

	// Stale: a full review of the SDD, then an edit of the PRD it implements (REQ-056).
	sdd := bs["payments-sdd"]
	if _, err := s.review(sdd.ID); err != nil {
		return fmt.Errorf("full review of payments-sdd: %w", err)
	}
	prdPath := filepath.Join(dir, "payments-prd", "PRD.md")
	if err := os.WriteFile(prdPath, append(prd, []byte("\nAn edit after the review of the SDD.\n")...), 0o644); err != nil {
		return err
	}
	if err := s.waitVersion(bs["payments-prd"].ID, bs["payments-prd"].Version.ID); err != nil {
		return err
	}
	if err := g.shot("verdict-stale", u("/bundles/"+sdd.ID+"?view=preview"), nil); err != nil {
		return err
	}

	// Build Ready with a waiver: ask for and approve a waiver of the SHOULD finding.
	w := bs["waived-prd"]
	var fs struct {
		Items []struct {
			ID    string `json:"id"`
			Level string `json:"level"`
		}
	}
	if err := s.call("GET", "/runs/"+w.Verdict.RunID+"/findings", nil, &fs); err != nil {
		return err
	}
	waivedOne := false
	for _, f := range fs.Items {
		if f.Level != "SHOULD" {
			continue
		}
		var wv struct{ ID string }
		if err := s.call("POST", "/bundles/"+w.ID+"/waivers", map[string]string{"finding_id": f.ID, "reason": "The payment methods are listed in the provider contract."}, &wv); err != nil {
			return err
		}
		if err := s.call("POST", "/waivers/"+wv.ID+"/approve", nil, nil); err != nil {
			return err
		}
		waivedOne = true
		break
	}
	if !waivedOne {
		return errors.New("waived-prd has no SHOULD finding to waive")
	}
	time.Sleep(time.Second)
	if err := g.shot("verdict-waivers", u("/bundles/"+w.ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := g.shot("verdict-build-ready", u("/bundles/"+bs["audit-sdd"].ID+"?view=preview"), nil); err != nil {
		return err
	}
	if err := g.shot("verdict-not-build-ready", u("/bundles/"+bs["restated-sdd"].ID+"?view=preview"), nil); err != nil {
		return err
	}

	// The bundle view with every overlay layer on (BUILD.md §6.2), in its three views, and the
	// preview with the default layers that a reader first sees.
	for _, view := range []string{"preview", "code", "split"} {
		if err := g.shot("bundle-"+view, u("/bundles/"+draft.ID+"?view="+view), func() error {
			if _, err := g.ab("eval", `localStorage.setItem('speccy.overlay', JSON.stringify(["risk","ambiguous","contradicted","unverified","slop"]))`); err != nil {
				return err
			}
			_, err := g.ab("reload")
			g.settle()
			return err
		}); err != nil {
			return err
		}
	}
	if err := g.shot("bundle-default", u("/bundles/"+draft.ID+"?view=preview"), func() error {
		if _, err := g.ab("eval", `localStorage.removeItem('speccy.overlay')`); err != nil {
			return err
		}
		_, err := g.ab("reload")
		g.settle()
		return err
	}); err != nil {
		return err
	}
	draft, err = s.bundle(draft.ID)
	if err != nil {
		return err
	}
	if err := g.shot("run-report", u("/bundles/"+draft.ID+"/runs/"+draft.Verdict.RunID), nil); err != nil {
		return err
	}

	// Tour: the first point, a middle point, and the last point.
	var tour struct{ Points []any }
	if err := s.call("GET", "/bundles/"+draft.ID+"/tour", nil, &tour); err != nil {
		return err
	}
	if len(tour.Points) < 3 {
		return fmt.Errorf("the draft PRD tour has %d points; the gauntlet needs 3", len(tour.Points))
	}
	press := func(n int) func() error {
		return func() error {
			for i := 0; i < n; i++ {
				if _, err := g.ab("press", "j"); err != nil {
					return err
				}
			}
			_, err := g.ab("wait", "900")
			return err
		}
	}
	for _, p := range []struct {
		name string
		n    int
	}{{"tour-first", 0}, {"tour-middle", len(tour.Points) / 2}, {"tour-last", len(tour.Points) - 1}} {
		if err := g.shot(p.name, u("/bundles/"+draft.ID+"/tour"), press(p.n)); err != nil {
			return err
		}
	}

	if err := g.shot("trace-matrix", u("/bundles/"+bs["payments-prd"].ID+"/trace"), nil); err != nil {
		return err
	}
	if err := g.shot("inbox-full", u("/inbox"), nil); err != nil {
		return err
	}

	// Diff with the AI summary: an edit of the draft PRD makes a second version.
	draftPath := filepath.Join(dir, "draft-prd", "PRD.md")
	text, err := os.ReadFile(draftPath)
	if err != nil {
		return err
	}
	edited := strings.Replace(string(text), "TBD", "Members of the loyalty programme who buy at least once a month.", 1)
	if err := os.WriteFile(draftPath, []byte(edited), 0o644); err != nil {
		return err
	}
	from := draft.Version.ID
	if err := s.waitVersion(draft.ID, from); err != nil {
		return err
	}
	now, err := s.bundle(draft.ID)
	if err != nil {
		return err
	}
	// The first capture asks the model; the others read the cached summary.
	return g.shot("diff-summary", u(fmt.Sprintf("/bundles/%s/diff?from=%s&to=%s", draft.ID, from, now.Version.ID)), func() error {
		if _, err := g.ab("find", "role", "button", "click", "--name", "Summarize the change"); err != nil {
			return err
		}
		_, err := g.ab("wait", "--text", "Fixed", "--timeout", "180000")
		return err
	})
}

// hosted captures the screens that exist only in hosted mode: the guest view and the invites.
func (g *gauntlet) hosted(root, bin, doc string) error {
	compose := func(args ...string) error {
		cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, b)
		}
		return nil
	}
	if err := compose("up", "-d", "--wait", "postgres"); err != nil {
		return err
	}
	if err := compose("exec", "-T", "postgres", "psql", "-U", "speccy", "-d", "postgres",
		"-c", "DROP DATABASE IF EXISTS speccy_gauntlet WITH (FORCE)", "-c", "CREATE DATABASE speccy_gauntlet"); err != nil {
		return err
	}
	env := []string{
		"SPECCY_DATABASE_URL=postgres://speccy:speccy@127.0.0.1:55432/speccy_gauntlet?sslmode=disable",
		"SPECCY_MASTER_KEY=c3BlY2N5LWRldi1vbmx5LW1hc3Rlci1rZXktMDAwMCE=",
	}
	s, err := start(bin, root, env, "serve", "--hosted")
	if err != nil {
		return err
	}
	defer s.stop()

	// The first admin (REQ-083): accept the invite in the browser, so the browser is signed in.
	cmd := exec.Command(bin, "admin", "invite", "--role", "admin")
	cmd.Env = append(append(os.Environ(), env...), "SPECCY_BASE_URL="+s.base)
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("admin invite: %w", err)
	}
	link := regexp.MustCompile(`https?://\S+`).FindString(string(b))
	token := regexp.MustCompile(`[#?&]token=([^&\s]+)`).FindStringSubmatch(link)
	if token == nil {
		return fmt.Errorf("no invite token in %q", b)
	}
	tok := token[1]
	res, err := http.Post(s.base+"/api/auth/speccy/invites/accept", "application/json",
		strings.NewReader(fmt.Sprintf(`{"token":%q,"name":"Ada Admin","email":"ada@example.com","password":"gauntlet-password-1"}`, tok)))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("accept the invite: %d", res.StatusCode)
	}
	var cookies []string
	for _, c := range res.Cookies() {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	s.cookie = strings.Join(cookies, "; ")
	if s.cookie == "" {
		return errors.New("accepting the invite set no session cookie")
	}

	// A bundle, a share link, and one open invite.
	if err := s.importFile(doc); err != nil {
		return err
	}
	bs, err := s.bundles()
	if err != nil {
		return err
	}
	var id string
	for _, b := range bs {
		id = b.ID
	}
	if err := s.call("PUT", "/bundles/"+id+"/visibility", map[string]string{"visibility": "link"}, nil); err != nil {
		return err
	}
	var share struct{ URL string }
	if err := s.call("POST", "/bundles/"+id+"/share", map[string]any{}, &share); err != nil {
		return err
	}
	if err := s.call("POST", "/admin/invites", map[string]string{"role": "member"}, nil); err != nil {
		return err
	}

	// The browser takes the admin's session cookies for the invites page.
	for _, c := range cookies {
		name, value, _ := strings.Cut(c, "=")
		if _, err := g.ab("cookies", "set", name, value, "--url", s.base); err != nil {
			return err
		}
	}
	if err := g.shot("admin-invites", s.base+"/admin", nil); err != nil {
		return err
	}
	if _, err := g.ab("cookies", "clear"); err != nil {
		return err
	}
	// A guest enters a display name first (REQ-086).
	return g.shot("guest-view", share.URL, func() error {
		if _, err := g.ab("cookies", "clear"); err != nil {
			return err
		}
		if _, err := g.ab("reload"); err != nil {
			return err
		}
		g.settle()
		if _, err := g.ab("fill", "#name", "Gia Guest"); err != nil {
			return err
		}
		if _, err := g.ab("press", "Enter"); err != nil {
			return err
		}
		g.settle()
		return nil
	})
}

// importFile imports one markdown file as a bundle (REQ-008).
func (s *server) importFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	boundary := "gauntletboundary"
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=%q\r\nContent-Type: text/markdown\r\n\r\n", boundary, filepath.Base(path))
	body.Write(content)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", boundary)
	req, err := http.NewRequestWithContext(context.Background(), "POST", s.base+"/api/v1/bundles/import", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Cookie", s.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(bufio.NewReader(res.Body))
		return fmt.Errorf("import: %d %s", res.StatusCode, b)
	}
	return nil
}
