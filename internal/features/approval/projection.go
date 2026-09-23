package approval

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/store"
)

// Projection keeps bundle_status_view and bundle_reviewer in step with the status streams
// (T-031). bundle_reviewer holds the named members of a private bundle (REQ-084, REQ-090).
func Projection(ctx context.Context, tx store.Tx, snap es.Stream, _ []es.Recorded) error {
	var s State
	if err := json.Unmarshal(snap.State, &s); err != nil {
		return err
	}
	q := tx.Queries()
	approvals := s.Approvals
	if approvals == nil {
		approvals = []Approval{}
	}
	raw, _ := json.Marshal(approvals)
	p := pgdb.UpsertSpecDocStatusViewParams{SpecDocID: snap.ID, Status: s.Current(), Approvals: dbtype.JSON(raw), UpdatedAt: snap.UpdatedAt}
	if s.ApprovedVersion != nil {
		p.ApprovedVersion = uuid.NullUUID{UUID: *s.ApprovedVersion, Valid: true}
	}
	if s.ReviewRequestedAt != nil {
		p.ReviewRequestedAt = sql.NullTime{Time: *s.ReviewRequestedAt, Valid: true}
	}
	if s.ApprovedAt != nil {
		p.ApprovedAt = sql.NullTime{Time: *s.ApprovedAt, Valid: true}
	}
	if err := q.UpsertSpecDocStatusView(ctx, p); err != nil {
		return err
	}
	for _, r := range s.Reviewers {
		if err := q.InsertSpecDocReviewer(ctx, pgdb.InsertSpecDocReviewerParams{SpecDocID: snap.ID, UserID: r}); err != nil {
			return err
		}
	}
	return nil
}
