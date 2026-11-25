package review

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
)

// Connector is a URL connector: a doc index, such as an llms.txt, on the web or in a GitHub
// repo, that the grounding stage and external links read with no MCP server
// (docs/specs/url-connectors.md).
type Connector interface {
	Fetcher
	// Owns says whether an address belongs to the connector: a link of its index that it may
	// fetch, or an external target it reads.
	Owns(u string) bool
	// Index is the address of the doc index.
	Index() string
	// Applies says whether the connector serves a doc of the profile.
	Applies(profile string) bool
}

// PromptConnectorPick asks which pages of a connector's index state the facts of the claims.
const PromptConnectorPick = "connector-pick-v1"

// maxPicks is the most pages one pick fetches for a batch of claims.
const maxPicks = 5

// maxIndexLinks is the most links of an index that a pick reads.
const maxIndexLinks = 1000

// indexLink is one link of a doc index.
type indexLink struct {
	url  string
	name string
	note string
}

var mdLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

// IndexLinkCount is how many links of an index a connector may fetch.
func IndexLinkCount(c Connector, text string) int { return len(indexLinks(c, text)) }

// indexLinks returns the links of an index that the connector owns, once each, with the text
// after the link on its line as the note. A relative link resolves against the index's own
// address.
func indexLinks(c Connector, text string) []indexLink {
	from, err := url.Parse(c.Index())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []indexLink
	for _, line := range strings.Split(text, "\n") {
		for _, m := range mdLink.FindAllStringSubmatchIndex(line, -1) {
			ref, err := url.Parse(line[m[4]:m[5]])
			if err != nil {
				continue
			}
			u := from.ResolveReference(ref)
			u.Fragment = ""
			abs := u.String()
			if !c.Owns(abs) || seen[abs] {
				continue
			}
			seen[abs] = true
			note := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line[m[1]:]), ":-–— "))
			out = append(out, indexLink{url: abs, name: line[m[2]:m[3]], note: note})
			if len(out) == maxIndexLinks {
				return out
			}
		}
	}
	return out
}

// Under says whether address u is under the base URL: on its host and scheme, with its path
// as a prefix.
func Under(u, base string) bool {
	pu, err1 := url.Parse(u)
	pb, err2 := url.Parse(base)
	if err1 != nil || err2 != nil || pu.Host == "" || !strings.EqualFold(pu.Host, pb.Host) || pu.Scheme != pb.Scheme {
		return false
	}
	prefix := pb.Path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return pu.Path+"/" == prefix || strings.HasPrefix(pu.Path, prefix) || prefix == "/"
}

// connectorFor returns the connector whose base URL holds target, or nil.
func connectorFor(conns []Connector, target string) Connector {
	for _, c := range conns {
		if c.Owns(target) {
			return c
		}
	}
	return nil
}

// connectors returns the connectors of the workspace that serve the doc's profile.
func (s *Service) connectors(ctx context.Context, profile string) ([]Connector, error) {
	if s.Connectors == nil {
		return nil, nil
	}
	all, err := s.Connectors(ctx)
	if err != nil {
		return nil, err
	}
	var out []Connector
	for _, c := range all {
		if profile == "" || c.Applies(profile) {
			out = append(out, c)
		}
	}
	return out, nil
}

// pageText is a fetched page as the cache keeps it: the last good text of a page stands in
// when a fetch fails.
type pageText struct {
	Text string `json:"text"`
}

// readPage fetches one page of a connector. A failed fetch gives the last good text and a run
// note. A page over maxAssetText, or one that is not text, is left out with a run note.
func (s *Service) readPage(ctx context.Context, rc *runCtx, c Connector, addr string) (string, bool) {
	key := cacheKey{Step: "connector-page", InputHash: hashOf(c.Name(), addr)}
	text, err := c.Fetch(ctx, addr)
	if err != nil {
		var last pageText
		if ok, _ := s.cached(ctx, key, &last); ok && last.Text != "" {
			rc.note(fmt.Sprintf("Speccy could not read %s through %s, so it used the text it read before: %s", addr, c.Name(), sentence(err.Error())))
			return last.Text, true
		}
		rc.note(fmt.Sprintf("Speccy could not read %s through %s: %s", addr, c.Name(), sentence(err.Error())))
		return "", false
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		rc.note(fmt.Sprintf("%s of %s is not a text page, so the grounding stage did not read it.", addr, c.Name()))
		return "", false
	}
	if len(text) > maxAssetText {
		rc.note(fmt.Sprintf("The grounding stage did not read %s of %s: it holds more than %d characters.", addr, c.Name(), maxAssetText))
		return "", false
	}
	_ = s.putCache(ctx, key, pageText{Text: text})
	return text, true
}

// connectorLabel is what a connector page says about one claim.
type connectorLabel struct {
	fileLabel
	connector string
}

// connectorClaims checks the claims that no file settled against the pages of each connector,
// in turn. For each batch, one call reads the connector's index and picks up to maxPicks pages,
// and the file check reads those pages with the word-for-word quote rule. A claim that one
// connector settles goes to no other.
func (s *Service) connectorClaims(ctx context.Context, rc *runCtx, in input, claims []claimFound, open []int, fingerprint string) (map[int]connectorLabel, error) {
	out := map[int]connectorLabel{}
	if len(open) == 0 {
		return out, nil
	}
	conns, err := s.connectors(ctx, in.profile.Profile.Key)
	if err != nil {
		return nil, fmt.Errorf("the connectors could not be read: %w", err)
	}
	for _, c := range conns {
		var left []int
		for _, i := range open {
			if _, ok := out[i]; !ok {
				left = append(left, i)
			}
		}
		if len(left) == 0 {
			break
		}
		rc.publish(Event{Type: "progress", Stage: StageGrounding, Message: "Reading " + c.Name()})
		index, ok := s.readPage(ctx, rc, c, c.Index())
		if !ok {
			continue
		}
		links := indexLinks(c, index)
		if len(links) == 0 {
			// An index with no link under the base URL is itself the only page.
			links = []indexLink{{url: c.Index(), name: c.Name()}}
		}
		indexHash := version.Hash([]byte(index))
		pages := map[string]claimSource{}
		for len(left) > 0 {
			n := min(verifyBatch, len(left))
			batch := left[:n]
			left = left[n:]
			picks, err := s.pickPages(ctx, rc, claims, batch, links, indexHash, fingerprint)
			if err != nil {
				return nil, err
			}
			var sources []claimSource
			for _, addr := range picks {
				src, ok := pages[addr]
				if !ok {
					text, read := s.readPage(ctx, rc, c, addr)
					if !read {
						continue
					}
					src = claimSource{path: addr, text: []byte(text)}
					pages[addr] = src
				}
				sources = append(sources, src)
			}
			if len(sources) == 0 {
				continue
			}
			sub := make([]claimFound, len(batch))
			for k, i := range batch {
				sub[k] = claims[i]
			}
			got, err := s.fileClaims(ctx, rc, sub, sources, fingerprint)
			if err != nil {
				return nil, err
			}
			for k, l := range got {
				out[batch[k]] = connectorLabel{fileLabel: l, connector: c.Name()}
			}
		}
	}
	return out, nil
}

// pickPages asks which links of the index most likely state the facts of the claims. The
// answer is cached by the claims and the index content.
func (s *Service) pickPages(ctx context.Context, rc *runCtx, claims []claimFound, batch []int, links []indexLink, indexHash, fingerprint string) ([]string, error) {
	if len(links) <= maxPicks {
		out := make([]string, len(links))
		for i, l := range links {
			out[i] = l.url
		}
		return out, nil
	}
	parts := []string{indexHash}
	for _, i := range batch {
		parts = append(parts, claims[i].text)
	}
	key := cacheKey{Step: "connector-pick", InputHash: hashOf(parts...), Fingerprint: fingerprint, PromptVersion: PromptConnectorPick}
	var got struct {
		Analysis string `json:"analysis"`
		Pages    []int  `json:"pages"`
	}
	ok, err := s.cached(ctx, key, &got)
	if err != nil {
		return nil, err
	}
	if ok {
		rc.hit()
	} else {
		texts := make([]string, len(batch))
		for k, i := range batch {
			texts[k] = claims[i].text
		}
		res, err := rc.call(ctx, s.Gateway, model.Call{
			Role: model.RoleReviewer, PromptVersion: PromptConnectorPick, System: systemPrompt,
			Prompt: pickPrompt(texts, links), Schema: pickSchema(len(links)), MaxTokens: 2000,
		})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(res.JSON, &got); err != nil {
			return nil, err
		}
		if err := s.putCache(ctx, key, got); err != nil {
			return nil, err
		}
	}
	var out []string
	seen := map[int]bool{}
	for _, p := range got.Pages {
		if p < 1 || p > len(links) || seen[p] || len(out) == maxPicks {
			continue
		}
		seen[p] = true
		out = append(out, links[p-1].url)
	}
	return out, nil
}

// pickPrompt lists the claims and the pages of the index, by number.
func pickPrompt(claims []string, links []indexLink) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Below are claims from a spec doc, and the pages of a doc index. Pick the pages, at most %d, that most likely state the facts of the claims, so that a reader can confirm or correct them. Pick by the page names and notes only. First say in \"analysis\" what the claims are about, in one sentence. Then give the numbers of the pages in \"pages\", the most likely first. Give no page when none fits.\n\n", maxPicks)
	for i, c := range claims {
		b.WriteString(data(fmt.Sprintf("Claim %d", i+1), c))
	}
	var idx strings.Builder
	for i, l := range links {
		fmt.Fprintf(&idx, "%d. %s", i+1, l.name)
		if l.note != "" {
			idx.WriteString(": " + l.note)
		}
		idx.WriteString("\n")
	}
	b.WriteString("\n" + data("Pages", idx.String()))
	return b.String()
}

// pickSchema puts "analysis" in front of "pages", which the keys of an answer sort after.
func pickSchema(n int) []byte {
	s := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"analysis", "pages"},
		"properties": map[string]any{
			"analysis": map[string]any{"type": "string"},
			"pages": map[string]any{"type": "array", "maxItems": maxPicks,
				"items": map[string]any{"type": "integer", "minimum": 1, "maximum": n}},
		},
	}
	out, _ := json.Marshal(s)
	return out
}

// connectorFinding records a claim that a connector page settles. The page is a source like a
// web page: a claim it confirms is verified, and a claim it contradicts is
// grounding.contradicted-claim with the page and its quote. The source policy does not apply:
// the admin chose the connector.
func (s *Service) connectorFinding(in input, ev *evaluation, c claimFound, cl connectorLabel, an anchor.Anchor, pol sourcepolicy.Policy) {
	class := sourcepolicy.Unclassified
	if len(pol.Classes) > 0 {
		class = pol.ClassOf(strings.Join(an.HeadingPath, "/"))
	}
	mode := "connector:" + cl.connector
	if cl.Label != "contradicted" {
		ev.claims = append(ev.claims, pendingClaim{text: c.text, label: "verified", reason: cl.Reason, class: class,
			sources: []sourcepolicy.Source{{URL: cl.File}}, anchor: an})
		ev.items = append(ev.items, verdict.Item{Slug: GroundingUnverified, Category: verdict.Evidence, Level: kernel.Should, Passed: true, Applicable: true})
		return
	}
	ev.claims = append(ev.claims, pendingClaim{text: c.text, label: "contradicted", reason: cl.Reason, class: class,
		sources: []sourcepolicy.Source{{URL: cl.File}}, anchor: an})
	lvl := in.level(GroundingContradicted, kernel.Must)
	ev.items = append(ev.items, verdict.Item{Slug: GroundingContradicted, Category: verdict.Evidence, Level: lvl, Applicable: true})
	ev.findings = append(ev.findings, pending{slug: GroundingContradicted, level: lvl, stage: StageGrounding, anchor: an,
		message: fmt.Sprintf("A source contradicts this claim: %q Source: %s", cl.Quote, cl.File),
		fix:     "Correct the claim, or explain why the source does not apply.",
		evidence: map[string]any{"claim": c.text, "reason": cl.Reason, "sources": []string{cl.File}, "search": mode, "class": class,
			"file": cl.File, "file_quote": cl.Quote}})
}
