// Package prreview reviews many spec pull requests in one batch, and keeps the reviewer's
// pending review on each one: the findings, the reviewer's own questions, and the commands
// that list, delete and discard what is pending (docs/specs/pr-review-batch.md). It runs in
// local mode only, because a pending review belongs to the person whose credential posts it.
package prreview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// LocalOnly is the answer of each operation of this package in hosted mode.
const LocalOnly = "Pending reviews post as you, so they run in local mode."

// DefaultParallel is how many pull requests of a batch run at the same time.
const DefaultParallel = 3

// MaxParallel is the most pull requests a batch runs at the same time.
const MaxParallel = 10

// API serves the batches, the questions to authors, and the pending reviews.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Reviews   *review.Service
	ReviewAPI *review.API
	Gateway   *model.Gateway
	Profiles  func() map[string]profile.Versioned
	// GitHub returns the client for a GitHub API with the local user's credential.
	GitHub func(ctx context.Context, apiURL string) (*github.Client, error)
	// LocalMode is false in hosted mode, where every operation answers LocalOnly.
	LocalMode bool
	// Life is the context of the owner process. A batch runs on it, so it goes on when the
	// call that started it ends.
	Life context.Context

	mu      sync.Mutex
	running map[uuid.UUID]*run
	plans   map[uuid.UUID]map[int]review.URLPlan
}

// run is a batch that runs in this process.
type run struct {
	mu        sync.Mutex
	cancelled bool
}

func (a *API) local() error {
	if !a.LocalMode {
		return kernel.Invalid("local_only", LocalOnly)
	}
	return nil
}

// Idle reports whether no batch runs in this process. An owner that wants to exit waits for it.
func (a *API) Idle() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.running) == 0
}

// pr is one pull request that a URL or a repo names.
type pr struct {
	build github.Build
	url   string
}

// parsePR reads the URL of a pull request.
func parsePR(raw string) (pr, error) {
	b, err := github.ParseBuildURL(strings.TrimSpace(raw))
	if err != nil || b.Pull == 0 {
		return pr{}, kernel.Invalid("bad_url", "%q is not the URL of a pull request.", raw)
	}
	return pr{build: b, url: prURL(b)}, nil
}

// parseRepo reads owner/name, or the URL of a repo.
func parseRepo(raw string) (github.Build, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://github.com/" + strings.Trim(raw, "/")
	}
	b, err := github.ParseBuildURL(raw)
	if err != nil {
		return b, kernel.Invalid("bad_repo", "%q is not a repo. Write it as owner/name.", raw)
	}
	return b, nil
}

func prURL(b github.Build) string {
	host := b.Host
	if host == "" {
		host = "github.com"
	}
	return fmt.Sprintf("https://%s/%s/pull/%d", host, b.Repo, b.Pull)
}

func (a *API) client(ctx context.Context, b github.Build) (*github.Client, error) {
	if a.GitHub == nil {
		return nil, kernel.Invalid("no_github", "This Speccy has no GitHub credential. Log in with gh auth login, or paste a token under Admin → GitHub.")
	}
	return a.GitHub(ctx, b.APIURL())
}

// noSpecDoc reports whether err says that the URL names no spec doc.
func noSpecDoc(err error) bool {
	ke, ok := kernel.AsError(err)
	return ok && ke.Code == "no_spec_doc"
}

// detail is the text of an error for a person.
func detail(err error) string {
	if ke, ok := kernel.AsError(err); ok {
		return ke.Detail
	}
	if errors.Is(err, model.ErrBudgetSpent) {
		return "The monthly token budget is spent."
	}
	return strings.TrimRight(err.Error(), ".") + "."
}

// inlineLimit is the repo's own limit of inline comments, at the commit the review read.
func inlineLimit(ctx context.Context, gh *github.Client, repo, commit string) int {
	raw, ok, err := gh.FileAt(ctx, repo, commit, source.RepoConfigFile)
	if err != nil || !ok {
		return 0
	}
	cfg, err := source.ParseRepoConfig(raw)
	if err != nil {
		return 0
	}
	return cfg.PR.InlineLimit
}

func (a *API) checks(key string) (profile.Profile, bool) {
	p, ok := a.Profiles()[key]
	return p.Profile, ok
}

// askID names one question of a reviewer, so the same question is not posted twice.
func askID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

var (
	markerRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	subRe    = regexp.MustCompile(`(?s)<sub>.*?</sub>`)
)

// excerpt is the first words of a comment, with no marker and no check line.
func excerpt(body string) string {
	body = subRe.ReplaceAllString(markerRe.ReplaceAllString(body, " "), " ")
	words := strings.Fields(body)
	if len(words) > 16 {
		return strings.Join(words[:16], " ") + " …"
	}
	return strings.Join(words, " ")
}

// mainDoc is the path of the spec doc of an item in its bundle, and its text.
func mainDoc(it review.URLItem) (string, []byte, bool) {
	rel := it.Doc.Path
	if it.Doc.Dir != "." && it.Doc.Dir != "" {
		rel = strings.TrimPrefix(it.Doc.Path, it.Doc.Dir+"/")
	}
	if it.Content.MainDoc != "" {
		rel = it.Content.MainDoc
	}
	for _, f := range it.Content.Files {
		if f.Path == rel {
			return rel, f.Content, true
		}
	}
	return rel, nil, false
}

func now() time.Time { return time.Now().UTC() }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// short is the first 7 characters of a commit SHA.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return kernel.NotFound("no_batch", "No batch has this ID.")
	}
	return err
}
