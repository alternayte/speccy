package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// PromptSameConflict matches the conflicts of a contradiction call with the conflicts of the
// last full review (#132).
const PromptSameConflict = "same-conflict-v1"

// priorConflict is one conflict of the last full review: its finding row, and the conflict as
// the review quoted it.
type priorConflict struct {
	row pgdb.Finding
	c   conflict
}

// priorConflicts returns the conflicts of the last full review of the doc with each linked doc,
// by the linked doc's ID, with the ones that review carried. A doc that a person asked a fresh
// review for after that review has none.
func (s *Service) priorConflicts(ctx context.Context, in input) (map[uuid.UUID][]priorConflict, error) {
	if in.version == uuid.Nil || in.bundle.ID == uuid.Nil {
		return nil, nil // content that is not saved has no review before it
	}
	q := s.DB.Queries()
	doc, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: in.bundle.ID})
	if err != nil {
		return nil, err
	}
	full, err := q.LatestFullReview(ctx, in.bundle.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if doc.FreshAt.Valid && !full.StartedAt.After(doc.FreshAt.Time) {
		return nil, nil
	}
	rows, err := q.ListFindings(ctx, full.ID)
	if err != nil {
		return nil, err
	}
	carried, err := carriedRows(ctx, q, full.ID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]priorConflict{}
	seen := map[string]bool{}
	for _, f := range append(rows, carried...) {
		if f.CheckSlug != ContradictionSlug {
			continue
		}
		var ev struct {
			UpstreamID    uuid.UUID `json:"upstream_bundle_id"`
			Quote         string    `json:"quote"`
			UpstreamQuote string    `json:"upstream_quote"`
			Explanation   string    `json:"explanation"`
		}
		if json.Unmarshal(f.Evidence, &ev) != nil || ev.UpstreamID == uuid.Nil || strings.TrimSpace(ev.Quote) == "" || strings.TrimSpace(ev.UpstreamQuote) == "" {
			continue
		}
		c := conflict{ThisQuote: ev.Quote, OtherQuote: ev.UpstreamQuote, Explanation: ev.Explanation}
		key := ev.UpstreamID.String() + "\x00" + c.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		out[ev.UpstreamID] = append(out[ev.UpstreamID], priorConflict{row: f, c: c})
	}
	return out, nil
}

// key is the folded identity of the conflict's two quotes, as a waiver matches them.
func (c conflict) key() string {
	return (&source.Conflict{Quote: c.ThisQuote, WithQuote: c.OtherQuote}).Key()
}

// keptConflict is one conflict a contradiction call ends with: a conflict that this call
// found, under the identity of the earlier conflict it matches, or an earlier conflict that
// this call did not find again and that is still in both docs. carried is the row of that one.
type keptConflict struct {
	c       conflict
	carried *pgdb.Finding
}

var sameConflictSchema = []byte(`{"type":"object","additionalProperties":false,"required":["matches"],"properties":{"matches":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["analysis","earlier","new"],"properties":{"analysis":{"type":"string"},"earlier":{"type":"integer"},"new":{"type":"integer"}}}}}}`)

// sameConflictPrompt asks which new conflict is an earlier conflict in other words. It is a
// call of its own: a contradiction prompt that also asks about the earlier conflicts makes the
// model call an open conflict fixed (#119).
func sameConflictPrompt(other string, earlier []conflict, found []conflict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Two reviews compared this doc with the doc %s, and each listed the statements of the two docs that conflict. The data parts below hold the earlier conflicts and the new conflicts, each with a number. A review often words the same conflict in other ways, and quotes a little more or a little less of the same text.\n", other)
	b.WriteString("For each new conflict, give the number of the earlier conflict that is the same conflict: the two docs disagree about the same fact, so that one fix ends both. Give 0 when no earlier conflict is the same. Two conflicts about different facts are different, even when they quote the same sentence.\n")
	b.WriteString("Answer once for each new conflict. Give an earlier number to one new conflict at most. In \"analysis\", first state the fact that each of the two conflicts is about.\n\n")
	list := func(cs []conflict) string {
		var l strings.Builder
		for i, c := range cs {
			fmt.Fprintf(&l, "%d: This doc says %q. The other doc says %q. Conflict: %s\n", i+1, c.ThisQuote, c.OtherQuote, strings.TrimSpace(c.Explanation))
		}
		return strings.TrimRight(l.String(), "\n")
	}
	b.WriteString(data("Earlier conflicts", list(earlier)))
	b.WriteString("\n")
	b.WriteString(data("New conflicts", list(found)))
	return b.String()
}

// withPriorConflicts gives each conflict that the call found the identity of the earlier
// conflict it is, and keeps each earlier conflict that the call did not find while both of its
// quotes are in their docs (#132). A conflict then leaves only through an edit, and not because
// the model words or misses it. The quotes of a conflict that the call found again are the
// earlier quotes, so a waiver or an upstream request of the conflict still matches it. A
// conflict quoted the same way as an earlier one is that one with no call; the rest go to a
// small call of their own.
func (s *Service) withPriorConflicts(ctx context.Context, rc *runCtx, in input, l linked, found []conflict, earlier []priorConflict, fingerprint string) ([]keptConflict, error) {
	var live []priorConflict
	for _, e := range earlier {
		_, _, ok1 := anchor.Find(in.main, e.c.ThisQuote)
		_, _, ok2 := anchor.Find(l.main, e.c.OtherQuote)
		if ok1 && ok2 {
			live = append(live, e)
		}
	}
	match := make([]int, len(found)) // the index in live + 1, or 0
	taken := make([]bool, len(live))
	for i, f := range found {
		for j, e := range live {
			if !taken[j] && e.c.key() == f.key() {
				match[i], taken[j] = j+1, true
				break
			}
		}
	}
	var newIdx, oldIdx []int
	for i := range found {
		if match[i] == 0 {
			newIdx = append(newIdx, i)
		}
	}
	for j := range live {
		if !taken[j] {
			oldIdx = append(oldIdx, j)
		}
	}
	if len(newIdx) > 0 && len(oldIdx) > 0 {
		var news, olds []conflict
		parts := []string{l.target.ID.String()}
		for _, j := range oldIdx {
			olds = append(olds, live[j].c)
			parts = append(parts, live[j].c.ThisQuote, live[j].c.OtherQuote, live[j].c.Explanation)
		}
		parts = append(parts, "\x00")
		for _, i := range newIdx {
			news = append(news, found[i])
			parts = append(parts, found[i].ThisQuote, found[i].OtherQuote, found[i].Explanation)
		}
		key := cacheKey{Step: "same-conflict", InputHash: hashOf(parts...), Fingerprint: fingerprint, PromptVersion: PromptSameConflict}
		var out struct {
			Matches []struct {
				Earlier int `json:"earlier"`
				New     int `json:"new"`
			} `json:"matches"`
		}
		ok, err := s.cached(ctx, key, &out)
		if err != nil {
			return nil, err
		}
		if ok {
			rc.hit()
		} else {
			res, err := rc.call(ctx, s.Gateway, model.Call{
				Role: model.RoleReviewer, PromptVersion: PromptSameConflict, System: systemPrompt,
				Prompt: sameConflictPrompt(l.target.Slug, olds, news), Schema: sameConflictSchema, MaxTokens: 4000,
			})
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(res.JSON, &out); err != nil {
				return nil, err
			}
			if err := s.putCache(ctx, key, out); err != nil {
				return nil, err
			}
		}
		for _, m := range out.Matches {
			if m.New < 1 || m.New > len(newIdx) || m.Earlier < 1 || m.Earlier > len(oldIdx) {
				continue
			}
			i, j := newIdx[m.New-1], oldIdx[m.Earlier-1]
			if match[i] != 0 || taken[j] {
				continue
			}
			match[i], taken[j] = j+1, true
		}
	}
	var out []keptConflict
	for i, f := range found {
		if match[i] == 0 {
			out = append(out, keptConflict{c: f})
			continue
		}
		e := live[match[i]-1].c
		f.ThisQuote, f.OtherQuote = e.ThisQuote, e.OtherQuote
		if strings.TrimSpace(e.Explanation) != "" {
			f.Explanation = e.Explanation
		}
		out = append(out, keptConflict{c: f})
	}
	for j, e := range live {
		if !taken[j] {
			row := e.row
			out = append(out, keptConflict{c: e.c, carried: &row})
		}
	}
	return out, nil
}
