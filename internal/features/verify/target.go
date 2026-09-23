package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// Resolved is the build a pasted target names: a repo at one commit, or a folder.
type Resolved struct {
	// Repo is owner/name, or the folder path when Folder is true.
	Repo   string
	SHA    string
	Branch string
	Pull   int
	Folder bool
	APIURL string
}

// resolve says which build a target names. A repo or a branch reads its head, a commit reads
// itself, and a pull request reads its head commit, in the repo the head lives in. The folder
// or the file in a URL names no scope, because the gate reads the whole tree.
func (a *API) resolve(ctx context.Context, target string) (Resolved, error) {
	target = strings.TrimSpace(target)
	if folder, ok := folderPath(target); ok {
		if !a.Local {
			return Resolved{}, kernel.Invalid("folder_in_hosted", "Paste a GitHub URL. The server cannot read your disk.")
		}
		info, err := os.Stat(folder)
		if err != nil || !info.IsDir() {
			return Resolved{}, kernel.Invalid("no_folder", "%s is not a folder on this machine.", folder)
		}
		return Resolved{Repo: folder, Folder: true}, nil
	}
	b, err := github.ParseBuildURL(target)
	if err != nil {
		return Resolved{}, kernel.Invalid("bad_url", "%s.", strings.TrimSuffix(err.Error(), "."))
	}
	if a.GitHub == nil {
		return Resolved{}, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot read %s.", b.Repo)
	}
	c, err := a.GitHub(ctx, b.APIURL())
	if err != nil {
		return Resolved{}, err
	}
	if c == nil {
		return Resolved{}, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot read %s.", b.Repo)
	}
	def, err := c.Repo(ctx, b.Repo)
	if err != nil {
		return Resolved{}, github.UnreadableRepo(b.Repo, err)
	}
	out := Resolved{Repo: b.Repo, APIURL: b.APIURL()}
	if b.Host == "github.com" || b.Host == "www.github.com" {
		out.APIURL = ""
	}
	switch {
	case b.SHA != "":
		sha, _, ok, err := c.LastCommit(ctx, b.Repo, b.SHA, "")
		if err != nil || !ok {
			return Resolved{}, kernel.Invalid("no_commit", "%s has no commit %s.", b.Repo, b.SHA)
		}
		out.SHA = sha
	case b.Pull > 0:
		pr, err := c.PullRequest(ctx, b.Repo, b.Pull)
		if err != nil {
			if github.IsNotFound(err) {
				return Resolved{}, kernel.Invalid("no_pull", "%s has no pull request %d.", b.Repo, b.Pull)
			}
			return Resolved{}, github.UnreadableRepo(b.Repo, err)
		}
		out.SHA, out.Pull = pr.HeadSHA, b.Pull
		if pr.Fork(b.Repo) {
			// The head commit lives in the fork, so the run reads the fork.
			out.Repo = pr.HeadRepo
		}
	default:
		out.Branch = b.Branch
		if out.Branch == "" {
			out.Branch = def
		}
		sha, _, err := c.Head(ctx, b.Repo, out.Branch)
		if err != nil {
			if github.IsNotFound(err) {
				return Resolved{}, kernel.Invalid("no_branch", "%s has no branch %s.", b.Repo, out.Branch)
			}
			return Resolved{}, github.UnreadableRepo(b.Repo, err)
		}
		out.SHA = sha
	}
	return out, nil
}

// folderPath reports whether a target is a folder on disk: an absolute path, or one under ~.
func folderPath(target string) (string, bool) {
	if rest, ok := strings.CutPrefix(target, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, rest), true
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), true
	}
	return "", false
}

// Default is one target that prefills the verify field, and where it came from.
type Default struct {
	Target string
	From   string // link or last_run
}

// defaults returns the targets that prefill the verify field: the repo of each implemented-by
// link, else the repo of the last run. A pasted target writes nothing into the doc, so the
// doc's link stays the record of where its code lives.
func (a *API) defaults(ctx context.Context, b pgdb.SpecDoc, main []byte) ([]Default, error) {
	var out []Default
	seen := map[string]bool{}
	fm, _, _ := source.ReadFrontmatter(main)
	for _, l := range fm.Links {
		if l.Kind != source.ExternalKind {
			continue
		}
		t, err := source.ParseExternalTarget(strings.TrimSpace(l.Target), nil)
		if err != nil || t.Scheme != source.GitHubScheme {
			continue
		}
		url := "https://" + t.Host + "/" + t.Repo
		if t.Commit != "" {
			url += "/commit/" + t.Commit
		}
		if !seen[url] {
			seen[url] = true
			out = append(out, Default{Target: url, From: "link"})
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	runs, err := a.DB.Queries().ListVerificationRuns(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.Status != StatusDone {
			continue
		}
		switch {
		case r.Sha != "":
			return []Default{{Target: "https://github.com/" + r.Repo, From: "last_run"}}, nil
		case a.Local:
			return []Default{{Target: r.Repo, From: "last_run"}}, nil
		}
	}
	return nil, nil
}
