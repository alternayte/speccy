package verify

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/engine/verify"
)

// testPathRe marks a file as a test by its path, which is how every language Speccy meets
// names one. A target's kind decides whether a test was cited, so the test must be
// recognised without a parser.
var testPathRe = regexp.MustCompile(`(^|/)(tests?|spec|specs|__tests__)/|(_test\.[a-z]+|\.test\.[a-z]+|\.spec\.[a-z]+|Test[A-Za-z0-9]*\.[a-z]+|test_[^/]+\.py)$`)

// isTestPath reports whether a path is a test file.
func isTestPath(p string) bool { return testPathRe.MatchString(p) }

// literalTargets finds every literal occurrence of a trace ID in the scanned files, and
// makes one target for each. This path costs no model call and repeats exactly.
func (a *API) literalTargets(ctx context.Context, repo Repo, ids []string) (map[string][]verify.Target, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[string][]verify.Target{}
	for _, path := range repo.Files() {
		content, ok, err := repo.Read(ctx, path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		text := string(content)
		for _, m := range idRe.FindAllStringIndex(text, -1) {
			id := text[m[0]:m[1]]
			if !want[id] {
				continue
			}
			line := lineAround(text, m[0])
			if strings.TrimSpace(line) == "" {
				continue
			}
			kind := verify.Code
			if isTestPath(path) {
				kind = verify.Test
			}
			t := verify.Target{Kind: kind, Path: path, Quote: line, Provenance: verify.FromLiteral}
			out[id] = append(out[id], verify.Check(t, content, true))
		}
	}
	for id := range out {
		sort.SliceStable(out[id], func(i, j int) bool {
			if out[id][i].Path != out[id][j].Path {
				return out[id][i].Path < out[id][j].Path
			}
			return out[id][i].Offset < out[id][j].Offset
		})
	}
	return out, nil
}

// idRe is REQ-051's ID pattern, as it appears inside code.
var idRe = regexp.MustCompile(`\b[A-Z]{2,6}-\d+\b`)

// lineAround returns the whole line that holds offset, trimmed of its indentation. The line
// is the anchor quote, because a bare ID appears in many files and an anchor must name one
// place.
func lineAround(text string, offset int) string {
	start := strings.LastIndexByte(text[:offset], '\n') + 1
	end := strings.IndexByte(text[offset:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += offset
	}
	return strings.TrimSpace(text[start:end])
}
