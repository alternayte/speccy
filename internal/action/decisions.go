package action

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// applied is what the Action did with the reply commands of one push.
type applied struct {
	decisions []Decision
	// threads holds the review threads to resolve, because their decision is written.
	threads []string
	// paste is the YAML for the author to paste, by sidecar path, when Speccy did not commit it.
	paste map[string]string
	// uncommitted says why Speccy did not commit the paste itself.
	uncommitted string
	// committed is the commit the Action made, or "".
	committed string
	// enforced are the checks this run took out of adoption mode.
	enforced []Command
}

// enforce commits the removal of each check that a conversation comment asked to turn back on
// (REQ-133). The commit goes to the pull request's branch, like any other decision.
func enforce(ctx context.Context, o Options) ([]Command, []string) {
	var warn []string
	comments, err := o.GitHub.IssueComments(ctx, o.Repo, o.PR)
	if err != nil {
		return nil, []string{"Speccy could not read the comments, so it enforced no check: " + err.Error()}
	}
	cmds := Enforcements(comments, o.Relaxed)
	if len(cmds) == 0 {
		return nil, nil
	}
	if o.Fork || o.HeadRef == "" {
		return nil, []string{"This pull request comes from a fork, so Speccy cannot turn a check back on. Remove the slug from " + source.RepoConfigFile + " yourself."}
	}
	src, done := o.Config, []Command{}
	for _, c := range cmds {
		next, ok, err := source.Enforce(src, c.Rest)
		if err != nil {
			warn = append(warn, err.Error())
			continue
		}
		if !ok {
			continue
		}
		src, done = next, append(done, c)
	}
	if len(done) == 0 {
		return nil, warn
	}
	slugs := make([]string, 0, len(done))
	for _, c := range done {
		slugs = append(slugs, c.Rest)
	}
	message := "Speccy: enforce " + strings.Join(slugs, ", ")
	if _, err := o.GitHub.Commit(ctx, o.Repo, o.HeadRef, message, []github.Change{{Path: source.RepoConfigFile, Content: src}}); err != nil {
		return nil, append(warn, "Speccy could not commit the adoption mode change: "+err.Error())
	}
	return done, warn
}

// apply turns the reply commands into sidecar entries and commits them to the pull request's
// branch (DEC-009). On a fork the Action has no write token, so it writes the sidecar into the
// summary comment instead, for the author to paste.
func apply(ctx context.Context, o Options, bundles []Bundle, threads []github.Thread) (applied, []string) {
	var warn []string
	out := applied{paste: map[string]string{}}
	targets := map[string]target{}
	for _, b := range bundles {
		for _, f := range b.Findings {
			targets[key(b.Slug, f.CheckSlug, f.Message, f.Anchor.Quote)] = target{bundle: b, finding: f}
		}
	}
	out.decisions = Decide(Commands(threads), targets)
	if len(out.decisions) == 0 {
		return out, nil
	}
	// One sidecar holds one doc, so the entries of one doc are written together.
	byDoc := map[string][]Decision{}
	var docs []string
	for _, d := range out.decisions {
		if d.Refused != "" || d.Doc == "" {
			continue
		}
		if _, ok := byDoc[d.Doc]; !ok {
			docs = append(docs, d.Doc)
		}
		byDoc[d.Doc] = append(byDoc[d.Doc], d)
	}
	sort.Strings(docs)
	var changes []github.Change
	var wrote []string
	for _, doc := range docs {
		dec, err := o.Sidecar(doc)
		if err != nil {
			warn = append(warn, fmt.Sprintf("Speccy could not read %s: %s", source.SidecarPath(doc), err))
			continue
		}
		for _, d := range byDoc[doc] {
			switch {
			case d.Waiver != nil:
				dec = dec.WithWaiver(*d.Waiver)
			case d.Trace != nil:
				dec = dec.WithTraceAck(*d.Trace)
			case d.Alone != nil:
				dec.Standalone = d.Alone
			}
			wrote = append(wrote, d.Command.Thread)
		}
		raw, err := dec.Marshal()
		if err != nil {
			warn = append(warn, fmt.Sprintf("Speccy could not write %s: %s", source.SidecarPath(doc), err))
			continue
		}
		changes = append(changes, github.Change{Path: source.SidecarPath(doc), Content: raw})
		out.paste[source.SidecarPath(doc)] = string(raw)
	}
	if len(changes) == 0 {
		return out, warn
	}
	if o.Fork || o.HeadRef == "" {
		// The summary comment carries the text to paste.
		out.uncommitted = "This pull request comes from a fork, so Speccy cannot commit to its branch."
		return out, warn
	}
	sha, err := o.GitHub.Commit(ctx, o.Repo, o.HeadRef, commitMessage(byDoc, docs), changes)
	if err != nil {
		warn = append(warn, "Speccy could not commit the decisions: "+err.Error())
		out.uncommitted = "Speccy could not commit to this pull request's branch: " + strings.TrimSuffix(err.Error(), ".") + "."
		return out, warn
	}
	out.committed, out.threads, out.paste = sha, wrote, map[string]string{}
	return out, warn
}

// commitMessage names what the commit records.
func commitMessage(byDoc map[string][]Decision, docs []string) string {
	n := 0
	for _, doc := range docs {
		n += len(byDoc[doc])
	}
	if n == 1 {
		d := byDoc[docs[0]][0]
		return fmt.Sprintf("Speccy: record a decision on %s", d.Doc)
	}
	return fmt.Sprintf("Speccy: record %d decisions", n)
}

// unmerged returns the waivers of a bundle's sidecar that the base branch does not have. They
// are the waivers that this pull request must merge before they count (DEC-009).
func unmerged(ctx context.Context, o Options, doc string) ([]source.Waiver, error) {
	head, err := o.Sidecar(doc)
	if err != nil || len(head.Waivers) == 0 {
		return nil, err
	}
	var base source.Decisions
	if o.BaseRef != "" {
		raw, ok, err := o.GitHub.FileAt(ctx, o.Repo, o.BaseRef, source.SidecarPath(doc))
		if err != nil {
			return nil, err
		}
		if ok {
			if base, err = source.ParseDecisions(raw); err != nil {
				return nil, err
			}
		}
	}
	var out []source.Waiver
	for _, w := range head.Waivers {
		if !slices.ContainsFunc(base.Waivers, func(b source.Waiver) bool {
			return b.Check == w.Check && slices.Equal(b.Section, w.Section) && b.SectionHash == w.SectionHash
		}) {
			out = append(out, w)
		}
	}
	return out, nil
}

// dependsOn reports whether an unmerged waiver covers a MUST finding of the bundle, which
// means the verdict holds only while that waiver is in this pull request.
func dependsOn(b Bundle, ws []source.Waiver) int {
	n := 0
	for _, w := range ws {
		if !slices.ContainsFunc(b.Findings, func(f api.Finding) bool {
			return f.Waived && f.CheckSlug == w.Check && slices.Equal(waiverSection(target{bundle: b, finding: f}), w.Section)
		}) {
			continue
		}
		if levelOf(b, w.Check) == api.FindingLevelMUST {
			n++
		}
	}
	return n
}

// levelOf is the level a check reported at in this run.
func levelOf(b Bundle, check string) api.FindingLevel {
	for _, f := range b.Findings {
		if f.CheckSlug == check {
			return f.Level
		}
	}
	return api.FindingLevelSHOULD
}

// pasteBlock is the sidecar text for the author to paste, after why Speccy did not commit it.
func pasteBlock(paste map[string]string, why string) string {
	if len(paste) == 0 {
		return ""
	}
	paths := make([]string, 0, len(paste))
	for p := range paste {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("\n" + why + " Put this in your branch:\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "\n`%s`:\n\n```yaml\n%s```\n", p, paste[p])
	}
	return b.String()
}
