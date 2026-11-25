// Package findingkey names a finding across pushes and review runs. A GitHub comment carries
// the key in a hidden marker, so Speccy finds its own comments again, skips a finding that a
// comment already holds, and shows a reply on the finding it answers.
package findingkey

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/http/api"
)

// Marker starts the hidden key marker of a comment: <!-- speccy:key:<key> -->.
const Marker = "<!-- speccy:key:"

// Key identifies a finding across pushes: the same finding on a moved line keeps its key.
func Key(slug, check, message, quote string) string {
	h := sha256.Sum256([]byte(slug + "\x00" + check + "\x00" + message + "\x00" + strings.Join(strings.Fields(quote), " ")))
	return hex.EncodeToString(h[:8])
}

// Thread is the key of a blocking thread of a spec doc.
func Thread(slug, threadID string) string { return Key(slug, "thread", threadID, "") }

// Keys returns the key of each finding, in the order of findings. Two findings with the same
// check, message and quote, such as two lower-case "must", would share a key: one comment would
// stand for both, and it would stay open after one of them is fixed. Each such finding takes
// the text of its line into its key, and the place among findings on equal lines. A finding
// with no twin keeps the plain key, so its open comment stays its own. files holds the files of
// the version by their path in the bundle.
func Keys(slug string, findings []api.Finding, files map[string][]byte) []string {
	keys := make([]string, len(findings))
	twins := map[string][]int{}
	for i, f := range findings {
		keys[i] = Key(slug, f.CheckSlug, f.Message, f.Anchor.Quote)
		twins[keys[i]] = append(twins[keys[i]], i)
	}
	for _, group := range twins {
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(x, y int) bool {
			a, c := findings[group[x]].Anchor, findings[group[y]].Anchor
			return a.File < c.File || (a.File == c.File && a.Start < c.Start)
		})
		seen := map[string]int{}
		for _, i := range group {
			f := findings[i]
			src := files[f.Anchor.File]
			text, _ := LineText(src, LineOf(src, f.Anchor.Start))
			k := Key(slug, f.CheckSlug, f.Message, f.Anchor.Quote+"\x00"+text)
			if n := seen[k]; n > 0 {
				keys[i] = Key(slug, f.CheckSlug, f.Message, fmt.Sprintf("%s\x00%s\x00%d", f.Anchor.Quote, text, n))
			} else {
				keys[i] = k
			}
			seen[k]++
		}
	}
	return keys
}

// In returns the key in the marker of a comment body, or "".
func In(body string) string {
	i := strings.Index(body, Marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(Marker):]
	if j := strings.Index(rest, " "); j >= 0 {
		return rest[:j]
	}
	return ""
}

// Mark is the marker line of key.
func Mark(key string) string { return Marker + key + " -->" }

// LineOf is the 1-based line of a byte offset in src.
func LineOf(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	if offset < 0 {
		offset = 0
	}
	return bytes.Count(src[:offset], []byte("\n")) + 1
}

// LineText is the text of a 1-based line of src.
func LineText(src []byte, line int) (string, bool) {
	lines := strings.Split(string(src), "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	return lines[line-1], true
}

// pullSuffix ends the slug of a spec doc of a pull request source, such as docs/prd-pr-42.
var pullSuffix = regexp.MustCompile(`(^|-)pr-[0-9]+$`)

// DocSlug is the slug that keys the findings of a spec doc: the slug the doc has on its repo's
// branch. A pull request source adds -pr-<n> to keep its bundles apart, and the keys drop it, so
// a comment of the App or the Action on the same pull request holds the same key.
func DocSlug(slug string) string {
	out := pullSuffix.ReplaceAllString(slug, "")
	if out == "" {
		return "."
	}
	return out
}
