package waiver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/store"
)

// Projection keeps waiver_view in step with the waiver streams (T-031).
func Projection(workspace uuid.UUID) es.Projection {
	return func(ctx context.Context, tx store.Tx, snap es.Stream, _ []es.Recorded) error {
		var s State
		if err := json.Unmarshal(snap.State, &s); err != nil {
			return err
		}
		section, _ := json.Marshal(nonNil(s.Section))
		approvals, _ := json.Marshal(nonNil(s.Approvals))
		return tx.Queries().UpsertWaiverView(ctx, pgdb.UpsertWaiverViewParams{
			ID: s.ID, WorkspaceID: workspace, BundleID: s.BundleID, CheckSlug: s.Check, Level: string(s.Level),
			SectionPath: dbtype.JSON(section), SectionHash: s.SectionHash, Reason: s.Reason, Status: s.Status,
			RequestedBy: s.RequestedBy, Approvals: dbtype.JSON(approvals), DecidedBy: s.DecidedBy,
			CreatedAt: firstTime(snap), UpdatedAt: snap.UpdatedAt,
		})
	}
}

// firstTime is the stream's time on its first append; later upserts keep created_at.
func firstTime(snap es.Stream) time.Time { return snap.UpdatedAt }

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
