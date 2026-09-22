package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alternayte/speccy/internal/source/github"
)

// ghScratchRepo holds the docs of docs/github.md. It is public, so a headless browser reads
// its pull request with no login, and no credential lives in this job.
const ghScratchRepo = "alternayte/speccy-guide"

// cmdDocsShotsGitHub captures the pictures of docs/github.md from a real pull request. It
// rebuilds the scratch repo, opens a pull request, runs speccy action against it so the
// comment is the product's own, screenshots the pull request, then closes it and deletes the
// branch. Nothing here is done by hand.
func cmdDocsShotsGitHub() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	bin := filepath.Join(root, "bin", "speccy")
	if _, err := os.Stat(bin); err != nil {
		return errors.New("bin/speccy is missing: run just build first")
	}
	ctx := context.Background()
	token, err := github.GHToken(ctx, "github.com")
	if err != nil {
		if errors.Is(err, github.ErrGHMissing) {
			return errors.New("the gh CLI is not installed. Install gh and run \"gh auth login\"")
		}
		return errors.New("gh is not logged in for github.com. Run \"gh auth login\"")
	}
	c := &github.Client{API: github.DefaultAPI, Token: token}
	base, err := c.Repo(ctx, ghScratchRepo)
	if err != nil {
		return fmt.Errorf("%s does not read: %w.\nCreate it first: gh repo create %s --public --add-readme",
			ghScratchRepo, err, ghScratchRepo)
	}

	work, err := os.MkdirTemp("", "speccy-gh-docs-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	files, err := scratchFiles(root)
	if err != nil {
		return err
	}
	// The default branch holds the specs as they stand, so the pull request changes one doc.
	if err := writeScratch(work, files); err != nil {
		return err
	}
	if _, err := c.Commit(ctx, ghScratchRepo, base, "Speccy docs: the specs this guide reads", changesOf(files)); err != nil {
		return fmt.Errorf("the scratch repo did not take the fixtures: %w", err)
	}
	head, _, err := c.Head(ctx, ghScratchRepo, base)
	if err != nil {
		return err
	}

	// The pull request changes the PRD, and takes the change in the checkout with it.
	edited := strings.Replace(files["docs/prd-payments.md"],
		"- **REQ-003:** The system SHOULD show \"Your payment is taking longer than usual\" after 5 seconds.",
		"- The system SHOULD show \"Your payment is taking longer than usual\" after 5 seconds.\n- TBD: what the page shows after the third retry.", 1)
	if edited == files["docs/prd-payments.md"] {
		return errors.New("the fixture PRD no longer holds the line the pull request edits")
	}
	branch := fmt.Sprintf("docs-shot-%s", time.Now().UTC().Format("20060102150405"))
	pr, err := c.Publish(ctx, ghScratchRepo, base, head, branch, "Payments PRD: the wait message",
		"Payments PRD: the wait message", "One doc changed, so Speccy reviews one bundle.",
		[]github.Change{{Path: "docs/prd-payments.md", Content: []byte(edited)}})
	if err != nil {
		return fmt.Errorf("the pull request did not open: %w", err)
	}
	prHead, _, err := c.Head(ctx, ghScratchRepo, branch)
	if err != nil {
		return err
	}
	fmt.Printf("docs-shots-github: pull request %d\n", pr.Number)
	defer func() {
		if err := c.ClosePullRequest(ctx, ghScratchRepo, pr.Number); err != nil {
			fmt.Println("docs-shots-github: the pull request stayed open:", err)
		}
		if err := c.DeleteBranch(ctx, ghScratchRepo, branch); err != nil {
			fmt.Println("docs-shots-github: the branch stayed:", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(work, "docs", "prd-payments.md"), []byte(edited), 0o644); err != nil {
		return err
	}

	// speccy action reads the Actions environment, so the comment on the pull request is the
	// one a workflow posts.
	if err := runAction(bin, work, token, pr.Number, prHead); err != nil {
		return err
	}

	out := filepath.Join(root, docsImages)
	d := &shots{out: out, session: "speccy-gh-docs"}
	defer func() { _, _ = d.ab("close") }()
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "1000"); err != nil {
		return err
	}
	summary, err := summaryComment(ctx, c, pr.URL, pr.Number)
	if err != nil {
		return err
	}
	// A short viewport on the comment's anchor: the picture holds the comment, and whatever
	// another app on the account posts below it stays out.
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "350"); err != nil {
		return err
	}
	if err := d.png("github-comment", summary, upABit(d)); err != nil {
		return err
	}
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "1000"); err != nil {
		return err
	}
	if err := d.png("github-inline", pr.URL+"/files", nil); err != nil {
		return err
	}

	// A reply asks for a waiver, and the next run commits it and resolves the thread.
	if err := replyToSpeccy(ctx, c, pr.Number); err != nil {
		return err
	}
	if err := runAction(bin, work, token, pr.Number, prHead); err != nil {
		return err
	}
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "385"); err != nil {
		return err
	}
	// The page is already open at this URL, so it needs a reload to show the updated comment.
	if err := d.png("github-decision", summary, func() error {
		if _, err := d.ab("reload"); err != nil {
			return err
		}
		if _, err := d.ab("wait", "2000"); err != nil {
			return err
		}
		_, err := d.ab("scroll", "up", "200")
		return err
	}); err != nil {
		return err
	}
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "700"); err != nil {
		return err
	}
	if err := d.png("github-commit", pr.URL+"/commits", nil); err != nil {
		return err
	}
	if err := adoptionShot(d, bin, work); err != nil {
		return err
	}
	fmt.Printf("docs-shots-github: %d pictures in %s\n", d.count, out)
	return nil
}

// adoptionShot captures docs/adoption.md's picture: the docs of a GitHub source that name no
// type, with the guess Speccy reads from the headings. It runs the real app against the
// scratch repo, so the list is the product's own.
func adoptionShot(d *shots, bin, work string) error {
	dir := filepath.Join(work, "adopt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s, err := start(bin, dir, nil, "serve", "--dir", dir)
	if err != nil {
		return err
	}
	defer s.stop()
	body := fmt.Sprintf(`{"url":"https://github.com/%s"}`, ghScratchRepo)
	res, err := http.Post(s.base+"/api/v1/github/sources", "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(res.Body)
		return fmt.Errorf("the source was not made: %s", strings.TrimSpace(string(out)))
	}
	if _, err := d.ab("set", "viewport", fmt.Sprint(shotWidth), "640"); err != nil {
		return err
	}
	return d.png("adopt-source", s.base+"/", func() error {
		_, err := d.ab("eval", "(function(){var h=document.querySelectorAll('h2');for(var i=0;i<h.length;i++){if(h[i].textContent.indexOf('name no type')>=0){h[i].scrollIntoView({block:'start'});return 'ok'}}return 'no section'})()")
		return err
	})
}

// upABit scrolls back over the sticky header, so the comment's own header is in the picture.
func upABit(d *shots) func() error {
	return func() error {
		_, err := d.ab("scroll", "up", "70")
		return err
	}
}

// summaryComment is the URL of Speccy's own summary comment on the pull request, so a picture
// shows the comment and not the top of the page.
func summaryComment(ctx context.Context, c *github.Client, prURL string, number int) (string, error) {
	comments, err := c.IssueComments(ctx, ghScratchRepo, number)
	if err != nil {
		return "", err
	}
	for _, cm := range comments {
		if strings.HasPrefix(cm.Body, "<!-- speccy:summary -->") {
			return fmt.Sprintf("%s#issuecomment-%d", prURL, cm.ID), nil
		}
	}
	return "", errors.New("the pull request has no Speccy summary comment")
}

// runAction runs speccy action in dir against the pull request, with the environment that
// GitHub Actions gives a job.
func runAction(bin, dir, token string, number int, headSHA string) error {
	event := filepath.Join(dir, "event.json")
	body, err := json.Marshal(map[string]any{"pull_request": map[string]any{
		"number": number, "head": map[string]any{"sha": headSHA},
	}})
	if err != nil {
		return err
	}
	if err := os.WriteFile(event, body, 0o644); err != nil {
		return err
	}
	cmd := exec.Command(bin, "action", "--stages", "lint")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GITHUB_TOKEN="+token,
		"GITHUB_REPOSITORY="+ghScratchRepo,
		"GITHUB_EVENT_PATH="+event,
		"GITHUB_API_URL="+github.DefaultAPI,
		"SPECCY_STATE_DIR="+filepath.Join(dir, ".speccy", "state"),
	)
	out, err := cmd.CombinedOutput()
	fmt.Print(indent(string(out)))
	// The verdict of the fixture is Not Build Ready, and advisory mode exits 0 for it.
	if err != nil {
		return fmt.Errorf("speccy action: %w", err)
	}
	return nil
}

// replyToSpeccy writes /speccy waive on the first Speccy review thread of the pull request.
func replyToSpeccy(ctx context.Context, c *github.Client, number int) error {
	threads, err := c.ReviewThreads(ctx, ghScratchRepo, number)
	if err != nil {
		return err
	}
	for _, t := range threads {
		if !strings.Contains(t.Body, "speccy:key:") || t.Resolved {
			continue
		}
		return c.ReplyToThread(ctx, t.ID, "/speccy waive The wait message moves to the checkout PRD, which ships first.")
	}
	return errors.New("the pull request has no open Speccy comment to reply to")
}

// scratchFiles is what the scratch repo holds: two linked specs and the configuration that
// maps them, from the fixtures the guide uses.
func scratchFiles(root string) (map[string]string, error) {
	prd, err := os.ReadFile(filepath.Join(root, "testdata", "bundles", "payments-prd", "PRD.md"))
	if err != nil {
		return nil, err
	}
	sdd, err := os.ReadFile(filepath.Join(root, "testdata", "bundles", "payments-sdd", "SPEC.md"))
	if err != nil {
		return nil, err
	}
	cfg := "# Written by speccy init --github.\nmap:\n  - glob: \"docs/prd-*.md\"\n    profile: prd\n" +
		"  - glob: \"docs/sdd-*.md\"\n    profile: sdd\nlink_rules:\n  - \"docs/sdd-{name}.md implements docs/prd-{name}.md\"\n" +
		"adoption:\n  relaxed:\n    - lint.passive-voice\n"
	// notes/ sits outside the mapping, so its docs name no type and no mapping covers them.
	// They are what the adoption picture shows.
	return map[string]string{
		"docs/prd-payments.md": strings.Replace(string(prd), "type: prd\n", "", 1),
		"docs/sdd-payments.md": strings.Replace(string(sdd), "target: payments-prd", "target: docs/prd-payments", 1),
		".speccy.yaml":         cfg,
		"notes/audit-trail.md": "# Audit trail\n\n## Context\n\nEvery refund writes a row.\n\n## Decisions\n\n- **DEC-001:** The audit log is append-only.\n\n## Interfaces\n\nThe writer takes a refund ID and returns nothing.\n",
		"notes/meeting.md":     "# Notes from Tuesday\n\nWe talked about refunds.\n",
	}, nil
}

func writeScratch(dir string, files map[string]string) error {
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func changesOf(files map[string]string) []github.Change {
	out := make([]github.Change, 0, len(files))
	for p, content := range files {
		out = append(out, github.Change{Path: p, Content: []byte(content)})
	}
	return out
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}
