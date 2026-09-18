package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
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
	mode := "none"
	var searcher Searcher
	switch {
	case native:
		mode = "native"
	case s.Search != nil:
		if searcher, err = s.Search(ctx); err != nil {
			return fmt.Errorf("the MCP search connection failed: %w", err)
		}
		if searcher != nil {
			mode = "mcp:" + searcher.Name()
		}
	}
	labels := make([]claimLabel, len(claims))
	if mode == "none" {
		rc.note(NoSearchNote)
		for i := range labels {
			labels[i] = claimLabel{Label: "unverified", Reason: "No search source is configured.", Sources: []string{}}
		}
	} else if err := s.labelClaims(ctx, rc, claims, labels, mode, searcher, fingerprint); err != nil {
		return err
	}

	for i, c := range claims {
		l := labels[i]
		an := anchor.New(in.bundle.MainDoc, in.main, in.doc, c.start, c.end)
		ev.claims = append(ev.claims, pendingClaim{text: c.text, label: l.Label, reason: l.Reason, sources: l.Sources, anchor: an})
		evidence := map[string]any{"claim": c.text, "reason": l.Reason, "sources": nonNil(l.Sources), "search": mode}
		switch l.Label {
		case "verified":
			ev.items = append(ev.items, verdict.Item{Category: verdict.Evidence, Level: kernel.Should, Passed: true, Applicable: true})
		case "contradicted":
			lvl := in.level(GroundingContradicted, kernel.Must)
			ev.items = append(ev.items, verdict.Item{Category: verdict.Evidence, Level: lvl, Applicable: true})
			ev.findings = append(ev.findings, pending{slug: GroundingContradicted, level: lvl, stage: StageGrounding, anchor: an,
				message: "A source contradicts this claim: " + sentence(l.Reason), fix: "Correct the claim, or explain why the source does not apply.", evidence: evidence})
		default:
			lvl := in.level(GroundingUnverified, kernel.Should)
			ev.items = append(ev.items, verdict.Item{Category: verdict.Evidence, Level: lvl, Applicable: true})
			ev.findings = append(ev.findings, pending{slug: GroundingUnverified, level: lvl, stage: StageGrounding, anchor: an,
				message: "No source confirms this claim.", fix: "Add a source or mark it as an assumption.", evidence: evidence})
		}
	}
	return nil
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
	results := make([][]string, len(jobs))
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
				Claims []string `json:"claims"`
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
		for _, text := range results[i] {
			text = strings.TrimSpace(text)
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
			out = append(out, claimFound{text: text, start: st, end: en})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out, nil
}

// labelClaims labels each claim with the given search mode. A claim's label is cached by its
// text, the search source, and the month, so a fact is checked again each month.
func (s *Service) labelClaims(ctx context.Context, rc *runCtx, claims []claimFound, labels []claimLabel, mode string, searcher Searcher, fingerprint string) error {
	month := time.Now().UTC().Format("2006-01")
	key := func(c claimFound) cacheKey {
		return cacheKey{Step: "verify", InputHash: hashOf(c.text), Fingerprint: fingerprint, PromptVersion: PromptVerify, Extra: mode + "|" + month}
	}
	var todo []int
	for i, c := range claims {
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
				note = "Search results from " + searcher.Name() + " follow each claim. Use only those results as sources."
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
			for k, idx := range batch {
				l, ok := got[k+1]
				if !ok {
					l = claimLabel{Label: "unverified", Reason: "The reviewer gave no label."}
				}
				// DEC-011: a label with no source is not verified or contradicted.
				if l.Label != "unverified" && len(l.Sources) == 0 {
					l = claimLabel{Label: "unverified", Reason: "The reviewer named no source. " + l.Reason}
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
			rc.publish(Event{Type: "progress", Stage: StageGrounding, Message: "Checking claims", Done: d, Total: len(claims)})
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
