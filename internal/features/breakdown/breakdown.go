// Package breakdown proposes, checks, edits and pushes the breakdown of one Build Ready spec doc:
// epics, and work items under them (#154 to #159). A work item adds nothing the doc does not
// say: every criterion is a quote from the doc.
package breakdown

import (
	"github.com/alternayte/speccy/internal/queue"

	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpclient"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the breakdown endpoints for one workspace.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	Gateway   *model.Gateway
	// Reviews resolves the doc's linked bundles.
	Reviews *review.Service
	// Questions lists a run's build questions with the readers' answers.
	Questions *review.API
	People    kernel.Directory
	// Connection opens the admin MCP connection for a tracker host, or returns ok false when
	// the admin configured none.
	Connection func(ctx context.Context, host string) (mcpclient.Connection, bool, error)
	// Hosts lists the hosts the admin MCP connections name, to offer a Jira site to push to.
	Hosts func(ctx context.Context) ([]string, error)
	// GitHub returns the client for a GitHub host, as the review service gets it.
	GitHub func(ctx context.Context, apiURL string) (*github.Client, error)
	// BaseURL is the address people open Speccy at, for the work item URL in an issue.
	BaseURL string
	// Queue runs the proposal jobs.
	Queue *queue.Queue
	// Now is the clock, for tests.
	Now func() time.Time
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// Body is what the breakdown row holds besides its version: the epics, the items, and the
// answers that close a coverage finding.
type Body struct {
	Epics []Epic `json:"epics"`
	Items []Item `json:"items"`
	// Covered holds the trace IDs a person said need no work item, with the answer.
	Covered []Cover `json:"covered"`
	// Defs is the text of each trace ID definition at the version the breakdown quotes, so a new
	// version can name the items whose trace IDs changed.
	Defs map[string]string `json:"defs"`
	// NextItem and NextEpic number new items and epics, so an ID never comes back after a delete.
	NextItem int `json:"next_item"`
	NextEpic int `json:"next_epic"`
}

// Epic is one group of work items.
type Epic struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Default is the assignee every item of this epic with no assignee of its own gets.
	Default *Assignee `json:"default,omitempty"`
}

// Item is one work item.
type Item struct {
	ID    string `json:"id"`
	Epic  string `json:"epic"`
	Title string `json:"title"`
	// TraceIDs are the doc's trace IDs this item builds. A doc with no trace IDs anchors the
	// item by HeadingPath instead.
	TraceIDs    []string `json:"trace_ids"`
	HeadingPath []string `json:"heading_path,omitempty"`
	// Criteria are the acceptance criteria, each a quote from the doc or a linked spec doc.
	Criteria []Criterion `json:"criteria"`
	// Repos are the repos the item changes, from the doc's implemented-by links.
	Repos     []string `json:"repos"`
	DependsOn []string `json:"depends_on"`
	// Questions are the open questions the proposal could not answer from the doc.
	Questions []string  `json:"questions"`
	Note      string    `json:"note,omitempty"`
	Assignee  *Assignee `json:"assignee,omitempty"`
	// Changed says what a new doc version changed in this item, until a person updates it.
	Changed []string `json:"changed,omitempty"`
	// Readers is the newest reader check of this item, while the item is as the readers read it.
	Readers *ReaderCheck `json:"readers,omitempty"`
}

// Criterion is one acceptance criterion: a quote, with the heading path it sits under.
type Criterion struct {
	Quote       string   `json:"quote"`
	HeadingPath []string `json:"heading_path"`
	// TraceID is the trace ID whose definition the quote is, if it is one.
	TraceID string `json:"trace_id,omitempty"`
	// Doc is the slug of the linked spec doc the quote comes from. Empty is this doc.
	Doc string `json:"doc,omitempty"`
}

// Assignee is a person or an agent in the tracker.
type Assignee struct {
	Kind string `json:"kind"` // person or agent
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Cover is a person's answer that a trace ID needs no work item.
type Cover struct {
	TraceID string `json:"trace_id"`
	// Answer is out_of_scope, covered_elsewhere or no_build.
	Answer string `json:"answer"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

// The answers that close a coverage finding.
const (
	CoverOutOfScope = "out_of_scope"
	CoverElsewhere  = "covered_elsewhere"
	CoverNoBuild    = "no_build"
)

// CoverAnswers lists the answers that close a coverage finding, in the order a screen offers
// them.
var CoverAnswers = []string{CoverOutOfScope, CoverElsewhere, CoverNoBuild}

// ReaderCheck is the result of the reader check on one item: what two readers would build from
// the item alone, and whether they read it the same way.
type ReaderCheck struct {
	// Hash is the item's content hash when the readers read it. An edit makes it stale.
	Hash     string   `json:"hash"`
	Diverge  bool     `json:"diverge"`
	Readings []string `json:"readings"`
	Reason   string   `json:"reason"`
	At       string   `json:"at"`
}

// Stored is one breakdown as the store holds it.
type Stored struct {
	Row  pgdb.Breakdown
	Body Body
}

// load reads the breakdown of a spec doc. ok is false when the doc has none.
func (a *API) load(ctx context.Context, q store.Querier, docID uuid.UUID) (Stored, bool, error) {
	row, err := q.GetBreakdown(ctx, pgdb.GetBreakdownParams{WorkspaceID: a.Workspace, SpecDocID: docID})
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, false, nil
	}
	if err != nil {
		return Stored{}, false, err
	}
	s := Stored{Row: row}
	if err := json.Unmarshal(row.Body, &s.Body); err != nil {
		return Stored{}, false, err
	}
	return s, true, nil
}

// save writes an edited body. It fails with a conflict when someone else saved first.
func (a *API) save(ctx context.Context, q store.Querier, s Stored, basedOn int64) (Stored, error) {
	body, err := json.Marshal(s.Body)
	if err != nil {
		return Stored{}, err
	}
	n, err := q.UpdateBreakdown(ctx, pgdb.UpdateBreakdownParams{
		ID: s.Row.ID, VersionID: s.Row.VersionID, Verdict: s.Row.Verdict, Acknowledged: s.Row.Acknowledged,
		Body: body, UpdatedAt: a.now(), BasedOn: basedOn,
	})
	if err != nil {
		return Stored{}, err
	}
	if n == 0 {
		return Stored{}, kernel.Conflict("breakdown_changed",
			"Someone changed this breakdown after you read it. Read it again, then make your change.")
	}
	out, _, err := a.load(ctx, q, s.Row.SpecDocID)
	return out, err
}

// item returns the item with id, or nil.
func (b *Body) item(id string) *Item {
	for i := range b.Items {
		if b.Items[i].ID == id {
			return &b.Items[i]
		}
	}
	return nil
}

// epic returns the epic with id, or nil.
func (b *Body) epic(id string) *Epic {
	for i := range b.Epics {
		if b.Epics[i].ID == id {
			return &b.Epics[i]
		}
	}
	return nil
}
