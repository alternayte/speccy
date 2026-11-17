package breakdown

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store"
)

// Prompt versions of the reader check.
const (
	PromptRead    = "breakdown-read-1"
	PromptCompare = "breakdown-compare-1"
	systemRead    = "You are a builder. You read one work item and nothing else, and you say what you would build from it. " +
		"You never fill a gap with a guess you do not name."
	systemCompare = "You compare two builders' plans for one work item. You say whether they would build the same thing."
)

var readSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["analysis", "build"],
  "properties": { "analysis": { "type": "string" }, "build": { "type": "string" } }
}`)

var compareSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["analysis", "difference", "same"],
  "properties": {
    "analysis": { "type": "string" },
    "difference": { "type": "string" },
    "same": { "type": "boolean" }
  }
}`)

// hashItem is the hash of what a reader reads of an item. An edit that changes it makes the
// reader check stale.
func hashItem(it Item) string {
	raw, _ := json.Marshal(struct {
		Title     string      `json:"title"`
		TraceIDs  []string    `json:"trace_ids"`
		Heading   []string    `json:"heading_path"`
		Criteria  []Criterion `json:"criteria"`
		Questions []string    `json:"questions"`
		Note      string      `json:"note"`
	}{it.Title, it.TraceIDs, it.HeadingPath, it.Criteria, it.Questions, it.Note})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// itemText is what a reader reads: the work item alone, as an issue would hold it.
func itemText(it Item) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Title: %s\n", it.Title)
	if len(it.TraceIDs) > 0 {
		fmt.Fprintf(&sb, "Trace IDs: %s\n", strings.Join(it.TraceIDs, ", "))
	}
	if len(it.HeadingPath) > 0 {
		fmt.Fprintf(&sb, "Section: %s\n", strings.Join(it.HeadingPath, " > "))
	}
	sb.WriteString("Acceptance criteria:\n")
	for _, c := range it.Criteria {
		fmt.Fprintf(&sb, "- %s\n", oneLine(c.Quote))
	}
	if len(it.Questions) > 0 {
		sb.WriteString("Open questions:\n")
		for _, q := range it.Questions {
			fmt.Fprintf(&sb, "- %s\n", q)
		}
	}
	if it.Note != "" {
		fmt.Fprintf(&sb, "Note: %s\n", it.Note)
	}
	return sb.String()
}

// CheckReaders runs the reader check on one item: two readers each read only the item and say
// what they would build, and a judge says whether they would build the same thing. It costs
// three model calls, so it runs only when a person asks.
func (a *API) CheckReaders(ctx context.Context, docID uuid.UUID, itemID string, basedOn int64) (Stored, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, docID)
	if err != nil {
		return Stored{}, err
	}
	s, ok, err := a.load(ctx, q, b.ID)
	if err != nil {
		return Stored{}, err
	}
	if !ok {
		return Stored{}, noBreakdown()
	}
	it := s.Body.item(itemID)
	if it == nil {
		return Stored{}, kernel.NotFound("no_item", "The breakdown has no item %s.", itemID)
	}
	text := untrustedStart + "\n" + itemText(*it) + untrustedEnd
	var readings []string
	for _, role := range []string{model.RoleReader1, model.RoleReader2} {
		res, err := a.Gateway.Call(ctx, model.Call{
			Role: role, PromptVersion: PromptRead, System: systemRead, Schema: readSchema, MaxTokens: 1500,
			Prompt: "Read the work item below. Say what you would build from it, in at most five short lines: " +
				"the parts you would change, the data, the interface and the behaviour a user sees. " +
				"Name each point the item leaves open.\n\n" + text,
		})
		if err != nil {
			return Stored{}, err
		}
		var r struct {
			Build string `json:"build"`
		}
		if err := json.Unmarshal(res.JSON, &r); err != nil {
			return Stored{}, err
		}
		readings = append(readings, strings.TrimSpace(r.Build))
	}
	res, err := a.Gateway.Call(ctx, model.Call{
		Role: model.RoleJudge, PromptVersion: PromptCompare, System: systemCompare, Schema: compareSchema, MaxTokens: 1500,
		Prompt: "Two builders read the same work item. Would they build the same thing? They differ when a choice " +
			"changes the data model, a public interface, security or what a user sees. Different words for the same " +
			"thing do not differ. Give the difference in one sentence when they differ.\n\n" +
			text + "\n\nBuilder 1:\n" + untrustedStart + "\n" + readings[0] + "\n" + untrustedEnd +
			"\n\nBuilder 2:\n" + untrustedStart + "\n" + readings[1] + "\n" + untrustedEnd,
	})
	if err != nil {
		return Stored{}, err
	}
	var cmp struct {
		Same       bool   `json:"same"`
		Difference string `json:"difference"`
	}
	if err := json.Unmarshal(res.JSON, &cmp); err != nil {
		return Stored{}, err
	}
	// The check took time: read the newest revision, and write the result onto the item only if
	// the item is still as the readers read it.
	return a.edit(ctx, b.ID, basedOn, func(s *Stored, _ store.Querier) error {
		cur := s.Body.item(itemID)
		if cur == nil || hashItem(*cur) != hashItem(*it) {
			return kernel.Conflict("item_changed", "The item changed while the readers read it. Run the check again.")
		}
		cur.Readers = &ReaderCheck{Hash: hashItem(*cur), Diverge: !cmp.Same, Readings: readings,
			Reason: strings.TrimSpace(cmp.Difference), At: a.now().Format("2006-01-02T15:04:05Z")}
		return nil
	})
}

func noBreakdown() error {
	return kernel.NotFound("no_breakdown", "This doc has no breakdown. Propose one first.")
}
