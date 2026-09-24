package action

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// MinReason is the shortest reason a decision takes, as in the app (REQ-072).
const MinReason = 20

// commandLine matches a reply command in a Speccy review thread.
var commandLine = regexp.MustCompile(`(?m)^\s*/speccy\s+(waive|ack)\b\s*(.*)$`)

// enforceLine matches the ramp command in the pull request's conversation, where the summary
// comment offers it (REQ-133).
var enforceLine = regexp.MustCompile(`(?m)^\s*/speccy\s+enforce\s+(\S+)\s*$`)

// Enforcements returns the check slugs that a conversation comment asked to turn back on, in
// the order they were written. A slug that is not relaxed is left out, so the same comment on
// the next push asks for nothing.
func Enforcements(comments []github.IssueComment, relaxed []string) []Command {
	var out []Command
	for _, c := range comments {
		if strings.HasPrefix(c.Body, summaryMarker) {
			continue // Speccy's own summary offers the command; it does not ask for it
		}
		for _, m := range enforceLine.FindAllStringSubmatch(c.Body, -1) {
			if !slices.Contains(relaxed, m[1]) {
				continue
			}
			if slices.ContainsFunc(out, func(x Command) bool { return x.Rest == m[1] }) {
				continue
			}
			out = append(out, Command{Kind: "enforce", Rest: m[1], By: c.Author()})
		}
	}
	return out
}

// Command is one decision that a reply asked for.
type Command struct {
	Thread string // the review thread it came from
	Key    string // the finding the thread points at
	Kind   string // waive or ack
	Rest   string // the text after the command word
	By     string // the login that wrote the reply
}

// Commands returns the reply commands of the Speccy threads, oldest reply first. A thread with
// more than one command keeps the last, because a person who writes a second reply means it.
// A resolved thread is left out: a run that applied its command, or found its finding gone,
// resolved it, so a command applies once.
func Commands(threads []github.Thread) []Command {
	var out []Command
	for _, t := range threads {
		k := keyIn(t.Body)
		if k == "" || t.Resolved {
			continue
		}
		var last *Command
		for _, r := range t.Replies {
			m := commandLine.FindStringSubmatch(r.Body)
			if m == nil {
				continue
			}
			last = &Command{Thread: t.ID, Key: k, Kind: m[1], Rest: strings.TrimSpace(m[2]), By: r.Author}
		}
		if last != nil {
			out = append(out, *last)
		}
	}
	return out
}

// Decision is a command turned into a sidecar entry, or the reason it was refused.
type Decision struct {
	Command Command
	Bundle  string // the bundle slug
	Doc     string // the main doc's path in the repo
	Waiver  *source.Waiver
	Trace   *source.TraceAck
	Alone   *source.Standalone
	Refused string // why the Action wrote nothing
	Line    string // one line for the summary comment
}

// target is the finding a command points at.
type target struct {
	bundle  Bundle
	finding api.Finding
}

// Decide turns the commands into sidecar entries. It refuses a command whose finding is gone,
// whose reason is too short, or whose check takes no acknowledgement.
func Decide(cmds []Command, targets map[string]target) []Decision {
	out := make([]Decision, 0, len(cmds))
	for _, c := range cmds {
		d := Decision{Command: c}
		t, ok := targets[c.Key]
		if !ok {
			d.Refused = "the finding it answers is gone, so nothing was written"
			d.Line = fmt.Sprintf("- `/speccy %s` by @%s: %s.", c.Kind, c.By, d.Refused)
			out = append(out, d)
			continue
		}
		d.Bundle, d.Doc = t.bundle.Slug, docPath(t.bundle)
		reason := c.Rest
		id := ""
		if c.Kind == "ack" && t.finding.CheckSlug == coverageSlug {
			id, reason = firstWord(reason)
			if !hasPrefixOf(id, t.bundle.CoverPrefixes) {
				d.Refused = fmt.Sprintf("it names no trace ID. Write `/speccy ack %s-001 <reason>`", prefixExample(t.bundle.CoverPrefixes))
			}
		}
		if d.Refused == "" && c.Kind == "ack" && t.finding.CheckSlug != coverageSlug && t.finding.CheckSlug != upstreamSlug {
			d.Refused = fmt.Sprintf("`%s` takes no acknowledgement. Use `/speccy waive <reason>`", t.finding.CheckSlug)
		}
		if d.Refused == "" && len(reason) < MinReason {
			d.Refused = fmt.Sprintf("the reason is shorter than %d characters", MinReason)
		}
		if d.Refused == "" {
			switch {
			case c.Kind == "ack" && t.finding.CheckSlug == coverageSlug:
				d.Trace = &source.TraceAck{ID: id, Status: "out_of_scope", Reason: reason, AcknowledgedBy: c.By}
			case c.Kind == "ack":
				d.Alone = &source.Standalone{Reason: reason, AcknowledgedBy: c.By}
			default:
				hash, ok := sectionHash(t)
				if !ok {
					d.Refused = "its section is not in the doc any more, so nothing was written"
					break
				}
				d.Waiver = &source.Waiver{Check: t.finding.CheckSlug, Section: waiverSection(t), Reason: reason,
					SectionHash: hash, RequestedBy: c.By}
			}
		}
		d.Line = summaryLine(d, t)
		out = append(out, d)
	}
	return out
}

// The checks an acknowledgement answers (SDD §9.4).
const (
	coverageSlug = "trace.coverage"
	upstreamSlug = "links.has-upstream"
)

// waiverSection is the section a waiver covers: the finding's heading path, or the whole doc
// for a doc-scope check, as in the app (§9.3).
func waiverSection(t target) []string {
	if t.finding.Anchor.HeadingPath == nil || t.bundle.DocScope[t.finding.CheckSlug] {
		return []string{}
	}
	return t.finding.Anchor.HeadingPath
}

// sectionHash is the hash of the section the waiver covers, now.
func sectionHash(t target) (string, bool) {
	main := t.bundle.Files[t.bundle.MainDoc]
	if main == nil {
		return "", false
	}
	return section.HashAt(section.Parse(main), main, waiverSection(t))
}

func summaryLine(d Decision, t target) string {
	what := "a waiver of `" + t.finding.CheckSlug + "`"
	if d.Trace != nil {
		what = "an acknowledgement of `" + d.Trace.ID + "`"
	} else if d.Alone != nil {
		what = "a standalone acknowledgement"
	}
	if d.Refused != "" {
		return fmt.Sprintf("- `/speccy %s` by @%s on `%s`: %s.", d.Command.Kind, d.Command.By, d.Bundle, d.Refused)
	}
	// The line holds for a commit and for a fork alike: the summary says below it whether
	// Speccy committed the sidecar or gives the text to paste.
	return fmt.Sprintf("- @%s asked for %s on `%s`. It goes in `%s`.", d.Command.By, what, d.Bundle, source.SidecarPath(d.Doc))
}

// docPath is the main doc's path in the repo.
func docPath(b Bundle) string {
	if b.Dir == "" {
		return b.MainDoc
	}
	return b.Dir + "/" + b.MainDoc
}

func firstWord(s string) (string, string) {
	first, rest, _ := strings.Cut(strings.TrimSpace(s), " ")
	return first, strings.TrimSpace(rest)
}

func hasPrefixOf(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(id, p+"-") {
			return true
		}
	}
	return false
}

func prefixExample(prefixes []string) string {
	if len(prefixes) > 0 {
		return prefixes[0]
	}
	return "REQ"
}
