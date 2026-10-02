package review

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
)

// Grounding finding slugs (REQ-032).
const (
	GroundingContradicted = "grounding.contradicted-claim"
	GroundingUnverified   = "grounding.unverified-claim"
)

// NoSearchNote is the run report note when no search source exists (REQ-034).
const NoSearchNote = "No search source is configured, so every claim is unverified. Use a backend with web search, or add an MCP connection marked search."

// NoPageNote is the run report note when a call with web search came back with no page (#121).
const NoPageNote = "The web search of the backend returned no page for some claims. Speccy accepts a source only when the search returned it, so those claims are unverified."

// internalReason is why a claim about an internal system has no label from the web (#121).
const internalReason = "The claim is about an internal system. A web search cannot confirm it, and can find another product with the same name."

const (
	minClaimWords = 12 // a shorter section is not worth a call
	verifyBatch   = 8
)

// Assumptions returns the byte ranges of the sentences that start with "Assumption:"
// (REQ-033), also in bold or as a list item. Claim checks skip them.
func Assumptions(main []byte) [][2]int {
	var out [][2]int
	text := string(main)
	for off := 0; ; {
		i := strings.Index(text[off:], "Assumption:")
		if i < 0 {
			return out
		}
		start := off + i
		off = start + len("Assumption:")
		if start >= 2 && text[start-2:start] == "**" {
			start -= 2
		}
		if !startsSentence(text, start) {
			continue
		}
		end := len(text)
		for k := off; k < len(text); k++ {
			if text[k] == '\n' || ((text[k] == '.' || text[k] == '!' || text[k] == '?') && (k+1 == len(text) || text[k+1] == ' ' || text[k+1] == '\n')) {
				end = k + 1
				break
			}
		}
		out = append(out, [2]int{start, end})
	}
}

// startsSentence reports whether text[i] starts a line, a list item, or a sentence.
func startsSentence(text string, i int) bool {
	lineStart := strings.LastIndexByte(text[:i], '\n') + 1
	prefix := strings.TrimSpace(text[lineStart:i])
	if prefix == "" || prefix == "-" || prefix == "*" || prefix == "+" || strings.HasSuffix(prefix, ".") && isNumber(strings.TrimSuffix(prefix, ".")) {
		return true
	}
	return strings.HasSuffix(prefix, ".") || strings.HasSuffix(prefix, "!") || strings.HasSuffix(prefix, "?")
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func insideAssumption(ranges [][2]int, start int) bool {
	for _, r := range ranges {
		if start >= r[0] && start < r[1] {
			return true
		}
	}
	return false
}

type claimFound struct {
	text  string
	start int
	end   int
	// internal says the claim is about a system of the author's own organisation. It goes to
	// an MCP search connection only, and never to the web search of the backend (#121).
	internal bool
}

type claimLabel struct {
	Label   string   `json:"label"`
	Reason  string   `json:"reason"`
	Sources []string `json:"sources"`
}

// groundingStage extracts claims per section and labels each one (SDD §8.4, REQ-030 to REQ-034).
func (s *Service) groundingStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, fingerprint string, native bool) error {
	claims, err := s.extractClaims(ctx, rc, in, fingerprint)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		return nil
	}

	// REQ-034: the backend's own web search, then an MCP search connection, else unverified.
	// A claim about an internal system goes to the MCP search connection only.
	var external, internal []int
	for i, c := range claims {
		if c.internal {
			internal = append(internal, i)
		} else {
			external = append(external, i)
		}
	}
	var searcher Searcher
	if s.Search != nil && (!native || len(internal) > 0) {
		if searcher, err = s.Search(ctx); err != nil {
			return fmt.Errorf("the MCP search connection failed: %w", err)
		}
	}
	mcp := "none"
	if searcher != nil {
		mcp = "mcp:" + searcher.Name()
	}
	web := mcp
	if native {
		web = "native"
	}
	labels := make([]claimLabel, len(claims))
	modes := make([]string, len(claims))
	label := func(idx []int, mode, noSearch string) error {
		for _, i := range idx {
			modes[i] = mode
			labels[i] = claimLabel{Label: "unverified", Reason: noSearch, Sources: []string{}}
		}
		if mode == "none" || len(idx) == 0 {
			return nil
		}
		var sr Searcher
		if mode != "native" {
			sr = searcher
		}
		return s.labelClaims(ctx, rc, claims, labels, idx, mode, sr, fingerprint)
	}
	if web == "none" && len(external) > 0 {
		rc.note(NoSearchNote)
	}
	if err := label(external, web, "No search source is configured."); err != nil {
		return err
	}
	if err := label(internal, mcp, internalReason); err != nil {
		return err
	}

	pol := in.profile.Profile.Grounding.Sources
	resolverOn := s.resolverOn(ctx)
	if pol.Active() && !resolverOn {
		rc.note(ResolverOffNote)
	}
	now := time.Now().UTC()
	for i, c := range claims {
		l := labels[i]
		an := anchor.New(in.bundle.DocPath, in.main, in.doc, c.start, c.end)
		class, sources, policyReason := s.applyPolicy(ctx, rc, pol, resolverOn, l, an, now)
		if policyReason != "" {
			l.Label, l.Reason = "unverified", policyReason
		}
		ev.claims = append(ev.claims, pendingClaim{text: c.text, label: l.Label, reason: l.Reason, class: class, sources: sources, anchor: an})
		urls := sourceURLs(sources)
		evidence := map[string]any{"claim": c.text, "reason": l.Reason, "sources": urls, "search": modes[i], "class": class}
		switch l.Label {
		case "verified":
			ev.items = append(ev.items, verdict.Item{Slug: GroundingUnverified, Category: verdict.Evidence, Level: kernel.Should, Passed: true, Applicable: true})
		case "contradicted":
			lvl := in.level(GroundingContradicted, kernel.Must)
			ev.items = append(ev.items, verdict.Item{Slug: GroundingContradicted, Category: verdict.Evidence, Level: lvl, Applicable: true})
			// The message names the source, so the author can read it (#121).
			msg := "A source contradicts this claim: " + sentence(l.Reason)
			if len(urls) > 0 {
				msg += " Source: " + strings.Join(urls, ", ")
			}
			ev.findings = append(ev.findings, pending{slug: GroundingContradicted, level: lvl, stage: StageGrounding, anchor: an,
				message: msg, fix: "Correct the claim, or explain why the source does not apply.", evidence: evidence})
		default:
			lvl := in.level(GroundingUnverified, kernel.Should)
			ev.items = append(ev.items, verdict.Item{Slug: GroundingUnverified, Category: verdict.Evidence, Level: lvl, Applicable: true})
			msg := "No source confirms this claim."
			if c.internal && modes[i] == "none" {
				msg += " It is about an internal system, so Speccy did not search the web for it."
			}
			ev.findings = append(ev.findings, pending{slug: GroundingUnverified, level: lvl, stage: StageGrounding, anchor: an,
				message: msg, fix: "Add a source or mark it as an assumption.", evidence: evidence})
		}
	}
	return nil
}

// ResolverOffNote is the run report note when a profile has a source policy and the admin
// turned the metadata resolver off.
const ResolverOffNote = "The source resolver is off, so Speccy reads no redirect chain and no retrieval date. A rule that needs either one drops the source instead of passing it."

// resolverOn reports whether the admin left the metadata resolver on. It is on by default.
func (s *Service) resolverOn(ctx context.Context) bool {
	if s.Resolve == nil {
		return false
	}
	if s.ResolveSources == nil {
		return true
	}
	return s.ResolveSources(ctx)
}

// applyPolicy gives the claim its class, resolves and judges its sources, and returns the
// reason the claim becomes unverified when the policy leaves it with no source.
func (s *Service) applyPolicy(ctx context.Context, rc *runCtx, pol sourcepolicy.Policy, resolverOn bool,
	l claimLabel, an anchor.Anchor, now time.Time) (string, []sourcepolicy.Source, string) {

	class := sourcepolicy.Unclassified
	if len(pol.Classes) > 0 {
		class = pol.ClassOf(strings.Join(an.HeadingPath, "/"))
	}
	sources := make([]sourcepolicy.Source, 0, len(l.Sources))
	for _, raw := range l.Sources {
		var src sourcepolicy.Source
		if resolverOn {
			src = s.Resolve.Resolve(ctx, pol, raw)
		} else {
			src = sourcepolicy.Source{URL: raw}
		}
		if !src.Dropped {
			src = pol.Evaluate(src, class, resolverOn, now)
		}
		sources = append(sources, src)
	}
	if !pol.Active() {
		return class, sources, ""
	}
	if class == sourcepolicy.Unclassified {
		switch pol.Unclassified {
		case sourcepolicy.UnclassifiedRequire:
			for i := range sources {
				sources[i].Dropped = true
				sources[i].Reason = "The profile needs every claim to carry a class, and this section matches no class rule."
			}
		case sourcepolicy.UnclassifiedWarn:
			rc.note("A claim under " + strings.Join(an.HeadingPath, " / ") + " matches no class rule, so no evidence rule applies to it.")
		}
	}
	kept, reasons := sourcepolicy.Kept(sources)
	if len(l.Sources) > 0 && len(kept) == 0 {
		return class, sources, "The source policy refused every source. " + strings.Join(reasons, " ")
	}
	return class, sources, ""
}

// sourceURLs returns the addresses of the sources the policy kept, for a finding's evidence.
func sourceURLs(sources []sourcepolicy.Source) []string {
	out := []string{}
	for _, s := range sources {
		if s.Dropped {
			continue
		}
		if s.FinalURL != "" {
			out = append(out, s.FinalURL)
			continue
		}
		out = append(out, s.URL)
	}
	return out
}

// extractClaims asks the reviewer for the claims of each section with enough text. Each
// section's claims are cached by its hash, so an unchanged section is not sent again (T-021).
func (s *Service) extractClaims(ctx context.Context, rc *runCtx, in input, fingerprint string) ([]claimFound, error) {
	assumptions := Assumptions(in.main)
	type job struct {
		sec *section.Section
	}
	var jobs []job
	for i := range in.doc.Sections {
		sec := &in.doc.Sections[i]
		if len(strings.Fields(section.Normalize(sec.Own(in.main)))) >= minClaimWords {
			jobs = append(jobs, job{sec: sec})
		}
	}
	type claimOut struct {
		Text  string `json:"text"`
		About string `json:"about"`
	}
	results := make([][]claimOut, len(jobs))
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	var doneMu sync.Mutex
	done := 0
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := cacheKey{Step: "claims", InputHash: j.sec.Hash, ProfileVer: in.profile.Version, Fingerprint: fingerprint, PromptVersion: PromptClaims}
			var out struct {
				Claims []claimOut `json:"claims"`
			}
			ok, err := s.cached(ctx, key, &out)
			if err != nil {
				errs[i] = err
				return
			}
			if ok {
				rc.hit()
			} else {
				res, err := rc.call(ctx, s.Gateway, model.Call{
					Role: model.RoleReviewer, PromptVersion: PromptClaims, System: systemPrompt,
					Prompt: claimsPrompt(j.sec.Path, string(j.sec.Own(in.main))), Schema: claimsSchema, MaxTokens: 4000,
				})
				if err != nil {
					errs[i] = err
					return
				}
				if err := json.Unmarshal(res.JSON, &out); err != nil {
					errs[i] = err
					return
				}
				if err := s.putCache(ctx, key, out); err != nil {
					errs[i] = err
					return
				}
			}
			results[i] = out.Claims
			doneMu.Lock()
			done++
			d := done
			doneMu.Unlock()
			rc.publish(Event{Type: "progress", Stage: StageGrounding, Message: "Finding claims", Done: d, Total: len(jobs)})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	var out []claimFound
	for i, j := range jobs {
		base := j.sec.BodyStart
		own := in.main[base:j.sec.OwnEnd]
		for _, c := range results[i] {
			text := strings.TrimSpace(c.Text)
			if text == "" || seen[text] {
				continue
			}
			// A claim must be text from the section (REQ-043's rule, applied to claims).
			st, en, ok := anchor.Find(own, text)
			if !ok {
				continue
			}
			st, en = st+base, en+base
			if insideAssumption(assumptions, st) {
				continue // REQ-033
			}
			seen[text] = true
			out = append(out, claimFound{text: text, start: st, end: en, internal: c.About == "internal"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out, nil
}

// labelClaims labels the claims with the numbers in idx, with the given search mode. A claim's
// label is cached by its text, the search source, and the month, so a fact is checked again
// each month.
func (s *Service) labelClaims(ctx context.Context, rc *runCtx, claims []claimFound, labels []claimLabel, idx []int, mode string, searcher Searcher, fingerprint string) error {
	month := time.Now().UTC().Format("2006-01")
	key := func(c claimFound) cacheKey {
		return cacheKey{Step: "verify", InputHash: hashOf(c.text), Fingerprint: fingerprint, PromptVersion: PromptVerify, Extra: mode + "|" + month}
	}
	var todo []int
	for _, i := range idx {
		c := claims[i]
		ok, err := s.cached(ctx, key(c), &labels[i])
		if err != nil {
			return err
		}
		if ok {
			rc.hit()
			continue
		}
		todo = append(todo, i)
	}
	var batches [][]int
	for len(todo) > 0 {
		n := min(verifyBatch, len(todo))
		batches = append(batches, todo[:n])
		todo = todo[n:]
	}
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	var doneMu sync.Mutex
	done := 0
	for bi, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			texts := make([]string, len(batch))
			results := make([]string, len(batch))
			for k, idx := range batch {
				texts[k] = claims[idx].text
				if searcher != nil {
					// SDD §14.3: the search result is untrusted data; it goes inside a data block.
					r, err := searcher.Search(ctx, claims[idx].text)
					if err != nil {
						errs[bi] = fmt.Errorf("the MCP search failed: %w", err)
						return
					}
					results[k] = r
				}
			}
			note := "Use your web search tool to look for a source for each claim."
			if searcher != nil {
				note = "Search results from " + searcher.Name() + " follow each claim. Use only those results as sources. For a result with no URL, copy its title."
			}
			res, err := rc.call(ctx, s.Gateway, model.Call{
				Role: model.RoleReviewer, PromptVersion: PromptVerify, System: systemPrompt,
				Prompt: verifyPrompt(texts, note, results), Schema: verifySchema(len(batch)),
				Search: mode == "native", MaxTokens: 8000,
			})
			if err != nil {
				errs[bi] = err
				return
			}
			var out struct {
				Labels []struct {
					Claim int `json:"claim"`
					claimLabel
				} `json:"labels"`
			}
			if err := json.Unmarshal(res.JSON, &out); err != nil {
				errs[bi] = err
				return
			}
			got := map[int]claimLabel{}
			for _, l := range out.Labels {
				got[l.Claim] = l.claimLabel
			}
			if searcher == nil && len(res.Sources) == 0 {
				rc.note(NoPageNote)
			}
			for k, idx := range batch {
				l, ok := got[k+1]
				if !ok {
					l = claimLabel{Label: "unverified", Reason: "The reviewer gave no label."}
				}
				// DEC-011: a label with no source is not verified or contradicted. A source
				// counts when the search returned it: the backend reports those pages, and
				// the model can name a page that it did not read (#121).
				named := len(l.Sources)
				l.Sources = slices.DeleteFunc(l.Sources, func(src string) bool {
					if searcher != nil {
						return !inResults(results[k], src)
					}
					return !returned(res.Sources, src)
				})
				switch {
				case l.Label == "unverified" || len(l.Sources) > 0:
				case named == 0:
					l = claimLabel{Label: "unverified", Reason: "The reviewer named no source. " + l.Reason}
				default:
					l = claimLabel{Label: "unverified", Reason: "The reviewer named a source that the search did not return. " + l.Reason}
				}
				if l.Sources == nil {
					l.Sources = []string{}
				}
				labels[idx] = l
				if ok {
					if err := s.putCache(ctx, key(claims[idx]), l); err != nil {
						errs[bi] = err
						return
					}
				}
			}
			doneMu.Lock()
			done += len(batch)
			d := done
			doneMu.Unlock()
			rc.publish(Event{Type: "progress", Stage: StageGrounding, Message: "Checking claims", Done: d, Total: len(idx)})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// returned reports whether src is one of the pages that the backend's web search returned.
// Two addresses are the same page when the host, the path, and the query are the same.
func returned(pages []string, src string) bool {
	want := pageKey(src)
	if want == "" {
		return false
	}
	for _, p := range pages {
		if pageKey(p) == want {
			return true
		}
	}
	return false
}

// pageKey names the page of a URL without the scheme, a leading "www.", the fragment, and a
// last slash. It is empty for text that is not a URL.
func pageKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	key := strings.TrimPrefix(strings.ToLower(u.Host), "www.") + strings.TrimSuffix(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key
}

// inResults reports whether src is in the text that an MCP search returned for the claim: a
// URL of a result, or its title.
func inResults(results, src string) bool {
	src = strings.TrimSpace(src)
	return len(src) >= 4 && strings.Contains(results, src)
}
