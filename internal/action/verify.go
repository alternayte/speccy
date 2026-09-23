package action

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/github"
)

// VerifyBundle is one bundle's verification run, as the Action reports it.
type VerifyBundle struct {
	Slug  string
	Run   api.Verification
	Error string
}

// verifyMarker separates the verify summary comment from the review one, so the Action keeps
// one of each on a pull request.
const verifyMarker = "<!-- speccy:verify -->"

// RunVerify posts the trace ID table of each bundle as one summary comment, and an inline
// comment on each breached target that the pull request changed. A breached target outside the
// diff goes in the summary instead, because GitHub refuses a comment on an unchanged line.
func RunVerify(ctx context.Context, o Options, bundles []VerifyBundle, files []github.PRFile) Result {
	var res Result
	if o.InlineLimit <= 0 {
		o.InlineLimit = DefaultInlineLimit
	}
	changed := map[string]map[int]bool{}
	for _, f := range files {
		changed[f.Filename] = ChangedLines(f.Patch)
	}

	var post []github.ReviewComment
	rest := map[string][]string{}
	for _, b := range bundles {
		for _, o := range b.Run.Outcomes {
			if o.Outcome != api.Breached {
				continue
			}
			line := breachedTarget(o)
			if line == nil {
				rest[b.Slug] = append(rest[b.Slug], breachLine(o, ""))
				continue
			}
			at := 0
			if line.Line != nil {
				at = *line.Line
			}
			where := fmt.Sprintf("%s:%d", line.Path, at)
			if !changed[line.Path][at] {
				rest[b.Slug] = append(rest[b.Slug], breachLine(o, where))
				continue
			}
			post = append(post, github.ReviewComment{Path: line.Path, Line: at, Side: "RIGHT", Body: breachBody(o)})
		}
	}
	if len(post) > o.InlineLimit {
		for _, c := range post[o.InlineLimit:] {
			rest[""] = append(rest[""], c.Path)
		}
		post = post[:o.InlineLimit]
	}
	if len(post) > 0 {
		if err := o.GitHub.CreateReview(ctx, o.Repo, o.PR, o.HeadSHA,
			"Speccy verified this build against the spec.", post); err != nil {
			res.Warnings = append(res.Warnings, "Speccy could not post inline comments: "+err.Error())
		} else {
			res.Posted = len(post)
		}
	}

	res.Summary = verifySummaryComment(bundles, rest)
	if err := upsertMarked(ctx, o, verifyMarker, res.Summary, len(bundles) > 0); err != nil {
		res.Warnings = append(res.Warnings, "Speccy could not post the summary comment: "+err.Error())
	}
	for _, b := range bundles {
		conclusion, title := "success", "Verified"
		switch {
		case b.Error != "":
			conclusion, title = "neutral", "The verification failed"
		case b.Run.NotVerified() && o.Blocking:
			conclusion, title = "failure", "Not Verified"
			res.Failed = true
		case b.Run.NotVerified():
			conclusion, title = "neutral", "Not Verified"
		}
		if err := o.GitHub.CreateCheckRun(ctx, o.Repo, github.CheckRun{
			Name: "speccy verify: " + b.Slug, HeadSHA: o.HeadSHA, Conclusion: conclusion,
			Title: title, Summary: verifyTableFor(b)}); err != nil {
			res.Warnings = append(res.Warnings, "Speccy could not set the check for "+b.Slug+": "+err.Error())
		}
	}
	return res
}

// breachedTarget returns the target a breach points at, or nil when none holds.
func breachedTarget(o api.VerificationOutcome) *api.VerificationTarget {
	for i := range o.Targets {
		t := o.Targets[i]
		if t.Holds != nil && *t.Holds && t.Kind == api.Code {
			return &o.Targets[i]
		}
	}
	return nil
}

func breachBody(o api.VerificationOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s is breached.** Two judges agreed that this code contradicts the requirement.\n\n", o.TraceId)
	if o.RequirementQuote != nil && *o.RequirementQuote != "" {
		b.WriteString("The requirement says:\n> " + *o.RequirementQuote + "\n\n")
	}
	if o.Reason != nil && *o.Reason != "" {
		b.WriteString(*o.Reason + "\n")
	}
	return b.String()
}

func breachLine(o api.VerificationOutcome, where string) string {
	if where == "" {
		return o.TraceId + " is breached."
	}
	return o.TraceId + " is breached at " + where + "."
}

// verifySummaryComment is the one comment the Action keeps for the gate.
func verifySummaryComment(bundles []VerifyBundle, rest map[string][]string) string {
	var b strings.Builder
	b.WriteString(verifyMarker + "\n## Speccy — build verification\n\n")
	if len(bundles) == 0 {
		b.WriteString("This pull request changes no bundle that Speccy verifies.\n")
		return b.String()
	}
	for _, v := range bundles {
		fmt.Fprintf(&b, "### %s\n\n", v.Slug)
		if v.Error != "" {
			b.WriteString(v.Error + "\n\n")
			continue
		}
		b.WriteString(verifyTableFor(v))
		for _, line := range rest[v.Slug] {
			b.WriteString("\n" + line)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nSpeccy reads the code. It runs no code and no tests, so a cited test is a citation and not a pass.\n")
	return b.String()
}

// verifyTableFor is one bundle's trace ID table, as markdown.
func verifyTableFor(v VerifyBundle) string {
	var b strings.Builder
	verdict := "Verified"
	if v.Run.NotVerified() {
		verdict = "Not Verified"
	}
	at := v.Run.Sha
	if at == "" {
		at = "a folder"
	}
	fmt.Fprintf(&b, "**%s** — %s at `%s`\n\n", verdict, v.Run.Repo, short(at))
	c := v.Run.Counts
	fmt.Fprintf(&b, "%d implemented, %d untested, %d unproven, %d missing, %d breached",
		c.Implemented, c.Untested, c.Unproven, c.Missing, c.Breached)
	if c.Waived > 0 {
		fmt.Fprintf(&b, ", %d waived", c.Waived)
	}
	b.WriteString(".\n\n")
	b.WriteString("| Trace ID | Outcome | Where |\n|---|---|---|\n")
	rows := append([]api.VerificationOutcome(nil), v.Run.Outcomes...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TraceId < rows[j].TraceId })
	for _, o := range rows {
		where := ""
		for _, t := range o.Targets {
			if t.Holds != nil && *t.Holds {
				at := 0
				if t.Line != nil {
					at = *t.Line
				}
				where = fmt.Sprintf("`%s:%d`", t.Path, at)
				if t.Kind == api.Test {
					where += " (test cited)"
				}
				break
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", o.TraceId, o.Outcome, where)
	}
	if c.Skipped > 0 {
		fmt.Fprintf(&b, "\nThe gate skipped %d trace ID%s outside the profile's verify prefixes.\n", c.Skipped, plural(c.Skipped))
	}
	for _, n := range v.Run.Notes {
		b.WriteString("\n" + n + "\n")
	}
	return b.String()
}
