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
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// trend is what a full review fixed, left open and found new, against the full review before
// it. The verdict stores it, so the rail shows whether the doc got closer to Build Ready.
type trend struct {
	SinceRun     uuid.UUID `json:"since_run"`
	SinceVersion int64     `json:"since_version"`
	Fixed        int       `json:"fixed"`
	Open         int       `json:"open"`
	New          int       `json:"new"`
	// NewIDs are the findings of the new units, which the rail marks.
	NewIDs []string `json:"new_ids"`
}

// unit names what the trend counts: a check in a section, or a build question. The message of
// a finding is not part of it, because the model words one shortfall differently on each run,
// and a comparison by message would read it as one fixed and one new.
func unit(p profile.Profile, doc section.Doc, main []byte, slug, stage string, an anchor.Anchor, evidence []byte) string {
	if stage == StageDivergence {
		var ev struct {
			Number int `json:"number"`
		}
		if json.Unmarshal(evidence, &ev) == nil && ev.Number > 0 {
			return fmt.Sprintf("question\x00%d", ev.Number)
		}
	}
	path := an.HeadingPath
	if b, ok := p.Bind(slug, doc, main, an.HeadingPath); ok {
		path = b.Path
	}
	return slug + "\x00" + strings.Join(path, "\x00")
}

// counts reports whether a finding counts in the trend: an open MUST or SHOULD.
func counts(level string, waived bool) bool {
	return !waived && (kernel.Level(level) == kernel.Must || kernel.Level(level) == kernel.Should)
}

// trendOf compares the open units of a full review, given as the unit of each of its finding
// IDs, with the full review before it. It returns nil when there is none.
func trendOf(ctx context.Context, q store.Querier, b pgdb.SpecDoc, run pgdb.ReviewRun, p profile.Profile, now map[string]string) (*trend, error) {
	prev, err := q.PreviousFullReview(ctx, pgdb.PreviousFullReviewParams{SpecDocID: b.ID, Before: run.StartedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: prev.VersionID})
	if err != nil {
		return nil, err
	}
	files, err := version.Files(ctx, q, prev.VersionID)
	if err != nil {
		return nil, err
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			main = f.Content
		}
	}
	doc := section.Parse(main)
	rows, err := q.ListFindings(ctx, prev.ID)
	if err != nil {
		return nil, err
	}
	carried, err := carriedRows(ctx, q, prev.ID)
	if err != nil {
		return nil, err
	}
	before := map[string]bool{}
	for _, f := range append(rows, carried...) {
		if !counts(f.Level, f.Waived) {
			continue
		}
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		before[unit(p, doc, main, f.CheckSlug, f.Stage, an, f.Evidence)] = true
	}
	t := &trend{SinceRun: prev.ID, SinceVersion: ver.Number, NewIDs: []string{}}
	open := map[string]bool{}
	for id, u := range now {
		if !before[u] {
			t.NewIDs = append(t.NewIDs, id)
		}
		open[u] = true
	}
	for u := range open {
		if before[u] {
			t.Open++
		} else {
			t.New++
		}
	}
	for u := range before {
		if !open[u] {
			t.Fixed++
		}
	}
	return t, nil
}
