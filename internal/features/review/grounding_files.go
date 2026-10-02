package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// GroundingFileContradicts is the finding of a claim that a file of the bundle contradicts.
const GroundingFileContradicts = "grounding.file-contradicts-claim"

// PromptFiles checks claims against the files that the review run holds.
const PromptFiles = "files-v1"

// modeFiles is the search mode of a claim that a file settles.
const modeFiles = "files"

// fileBatch is the most claims one call checks against the files. Each call holds every
// file, so a larger batch costs fewer tokens than the batch of a web search.
const fileBatch = 20

// claimSource is one text that can settle a claim with no search: a file of the bundle, or
// the main doc of a linked spec doc.
type claimSource struct {
	path string
	text []byte
	// linked says the text is a linked spec doc. It can confirm a claim. A conflict with it
	// is the coherence stage's, so that one conflict gives one finding.
	linked bool
}

// claimSources returns the texts that the run already holds: the text files of the bundle
// other than the main doc and its sidecar, then the linked spec docs, up to maxAssetText. It
// also returns the paths that the bound left out.
func claimSources(in input) (sources []claimSource, leftOut []string) {
	total := 0
	add := func(path string, text []byte, linked bool) {
		if !utf8.Valid(text) || len(strings.TrimSpace(string(text))) == 0 {
			return
		}
		if total+len(text) > maxAssetText {
			leftOut = append(leftOut, path)
			return
		}
		total += len(text)
		sources = append(sources, claimSource{path: path, text: text, linked: linked})
	}
	for _, f := range in.files {
		if f.Path == in.bundle.DocPath || source.IsSidecar(f.Path) {
			continue
		}
		add(f.Path, f.Content, false)
	}
	seen := map[string]bool{}
	for _, l := range in.linked {
		if l.target == nil || seen[l.target.DocPath] {
			continue
		}
		seen[l.target.DocPath] = true
		add(l.target.DocPath, l.main, true)
	}
	return sources, leftOut
}

// fileLabel is what the files say about one claim, as cached.
type fileLabel struct {
	// Label is confirmed, contradicted, or silent.
	Label string `json:"label"`
	// Applies says the file speaks about the same thing as the claim.
	Applies bool   `json:"applies"`
	File    string `json:"file"`
	Quote   string `json:"quote"`
	Reason  string `json:"analysis"`
}

// fileLevel is the level of a claim that a file of the bundle contradicts: SHOULD, or the
// level the profile gives it.
func fileLevel(in input) kernel.Level {
	lvl := kernel.Should
	if strings.EqualFold(in.profile.Profile.Grounding.FileContradiction, string(kernel.Must)) {
		lvl = kernel.Must
	}
	return in.level(GroundingFileContradicts, lvl)
}

// filesPrompt asks what the files say about each claim.
func filesPrompt(claims []string, sources []claimSource) string {
	var b strings.Builder
	b.WriteString("Check each claim below against the files below. The files are the only sources: do not use what you know, and do not search. For each claim, first say in \"analysis\" what the files state about the fact of the claim, in one sentence.\n")
	b.WriteString("Then give \"applies\": true when a file speaks about the same system, product, feature, or team as the claim, and false when it does not. A thing of another product or another company is another thing, also when it has the same name: a claim about the Workflows feature of the author's own system is not about the Workflows page of a product that the file names.\n")
	b.WriteString("Then label the claim:\n")
	b.WriteString("- \"confirmed\" when a file states the same fact;\n")
	b.WriteString("- \"contradicted\" when a file states a different fact about the same thing: a different number, owner, name, or behaviour;\n")
	b.WriteString("- \"silent\" when no file states the fact.\n")
	b.WriteString("A file that does not mention the fact is silent: it does not contradict the claim. With \"applies\" false, the label is \"silent\". A file that adds detail to the claim does not contradict it. Most claims are silent.\n")
	b.WriteString("For \"confirmed\" and \"contradicted\", give in \"file\" the name of the file as it is written below, and in \"quote\" the one sentence of that file that states the fact, copied word for word. For \"silent\", give an empty \"file\" and an empty \"quote\".\n\n")
	for i, c := range claims {
		b.WriteString(data(fmt.Sprintf("Claim %d", i+1), c))
	}
	b.WriteString("\n")
	for _, s := range sources {
		b.WriteString(data("File "+s.path, string(s.text)))
	}
	return b.String()
}

// filesSchema puts "analysis" and "applies" in front of "label": the keys of an answer come in
// the order of the alphabet, and a label with no analysis in front of it is a guess. With no
// "applies", a file about another product with the same name contradicted a claim.
func filesSchema(n int) []byte {
	s := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"claims"},
		"properties": map[string]any{
			"claims": map[string]any{
				"type": "array", "minItems": n, "maxItems": n,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"analysis", "applies", "claim", "file", "label", "quote"},
					"properties": map[string]any{
						"analysis": map[string]any{"type": "string"},
						"applies":  map[string]any{"type": "boolean"},
						"claim":    map[string]any{"type": "integer", "minimum": 1, "maximum": n},
						"file":     map[string]any{"type": "string"},
						"label":    map[string]any{"type": "string", "enum": []string{"confirmed", "contradicted", "silent"}},
						"quote":    map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	out, _ := json.Marshal(s)
	return out
}

// settled returns the label that the files give a claim, or false when no file settles it. A
// label counts only when its quote is word for word in the file it names: a model can name
// text that it did not read. A linked spec doc confirms a claim and does not contradict one.
func settled(l fileLabel, sources []claimSource) (fileLabel, bool) {
	if l.Label != "confirmed" && l.Label != "contradicted" || !l.Applies {
		return fileLabel{}, false
	}
	for _, s := range sources {
		if s.path != strings.TrimSpace(l.File) {
			continue
		}
		if s.linked && l.Label == "contradicted" {
			return fileLabel{}, false
		}
		start, end, ok := anchor.Find(s.text, l.Quote)
		if !ok || strings.TrimSpace(l.Quote) == "" {
			return fileLabel{}, false
		}
		l.File, l.Quote = s.path, string(s.text[start:end])
		return l, true
	}
	return fileLabel{}, false
}

// fileClaims checks the claims against the texts that the run holds, before any search. It
// returns the label of each claim that a file settles, by the number of the claim. The label
// of a claim is cached by its text and the content of the files, with no month: a file
// changes only when its content changes.
func (s *Service) fileClaims(ctx context.Context, rc *runCtx, claims []claimFound, sources []claimSource, fingerprint string) (map[int]fileLabel, error) {
	out := map[int]fileLabel{}
	if len(sources) == 0 || len(claims) == 0 {
		return out, nil
	}
	parts := make([]string, 0, len(sources)*2)
	for _, src := range sources {
		kind := "file"
		if src.linked {
			kind = "linked"
		}
		parts = append(parts, kind+":"+src.path, version.Hash(src.text))
	}
	filesHash := hashOf(parts...)
	key := func(c claimFound) cacheKey {
		return cacheKey{Step: "files", InputHash: hashOf(c.text, filesHash), Fingerprint: fingerprint, PromptVersion: PromptFiles}
	}
	labels := make([]fileLabel, len(claims))
	var todo []int
	for i, c := range claims {
		ok, err := s.cached(ctx, key(c), &labels[i])
		if err != nil {
			return nil, err
		}
		if ok {
			rc.hit()
			continue
		}
		todo = append(todo, i)
	}
	var batches [][]int
	for len(todo) > 0 {
		n := min(fileBatch, len(todo))
		batches = append(batches, todo[:n])
		todo = todo[n:]
	}
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for bi, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			texts := make([]string, len(batch))
			for k, idx := range batch {
				texts[k] = claims[idx].text
			}
			res, err := rc.call(ctx, s.Gateway, model.Call{
				Role: model.RoleReviewer, PromptVersion: PromptFiles, System: systemPrompt,
				Prompt: filesPrompt(texts, sources), Schema: filesSchema(len(batch)), MaxTokens: 8000,
			})
			if err != nil {
				errs[bi] = err
				return
			}
			var got struct {
				Claims []struct {
					Claim int `json:"claim"`
					fileLabel
				} `json:"claims"`
			}
			if err := json.Unmarshal(res.JSON, &got); err != nil {
				errs[bi] = err
				return
			}
			for _, l := range got.Claims {
				if l.Claim < 1 || l.Claim > len(batch) {
					continue
				}
				idx := batch[l.Claim-1]
				labels[idx] = l.fileLabel
				if err := s.putCache(ctx, key(claims[idx]), l.fileLabel); err != nil {
					errs[bi] = err
					return
				}
			}
			rc.publish(Event{Type: "progress", Stage: StageGrounding, Message: "Reading the files of the bundle"})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	for i, l := range labels {
		if l, ok := settled(l, sources); ok {
			out[i] = l
		}
	}
	return out, nil
}
