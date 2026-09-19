package thread

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/store"
)

// Projection keeps thread_view and thread_message_view in step with the thread streams, in the
// append transaction (T-031).
func Projection(workspace uuid.UUID) es.Projection {
	return func(ctx context.Context, tx store.Tx, snap es.Stream, events []es.Recorded) error {
		var s State
		if err := json.Unmarshal(snap.State, &s); err != nil {
			return err
		}
		q := tx.Queries()
		status := "open"
		if !s.Open {
			status = "resolved"
		}
		last := s.CreatedAt
		if n := len(s.Messages); n > 0 {
			last = s.Messages[n-1].At
		}
		p := pgdb.UpsertThreadViewParams{
			ID: s.ID, WorkspaceID: workspace, ProfileKey: s.ProfileKey, AnchorKind: s.AnchorKind, Anchor: dbtype.JSON(s.Anchor),
			AddressedTo: s.AddressedTo, Title: s.Title, Blocking: s.Blocking, Status: status, CreatedBy: s.CreatedBy.ID,
			CreatedAt: s.CreatedAt, LastMessageAt: last, MessageCount: int64(len(s.Messages)),
		}
		if s.BundleID != nil {
			p.BundleID = uuid.NullUUID{UUID: *s.BundleID, Valid: true}
		}
		if err := q.UpsertThreadView(ctx, p); err != nil {
			return err
		}
		for _, e := range events {
			switch e.Type {
			case Opened, MessagePosted:
				m := s.Messages[0]
				if e.Type == MessagePosted {
					var pm postedV1
					if err := json.Unmarshal(e.Payload, &pm); err != nil {
						return err
					}
					m = pm.Message
				}
				sources, _ := json.Marshal(nonNil(m.Sources))
				if err := q.InsertThreadMessage(ctx, pgdb.InsertThreadMessageParams{
					ID: m.ID, ThreadID: s.ID, Seq: int64(m.Seq), AuthorKind: m.Author.Kind, AuthorID: m.Author.ID,
					AuthorName: m.Author.Name, Body: m.Body, Sources: dbtype.JSON(sources), Decision: "", CreatedAt: m.At,
				}); err != nil {
					return err
				}
			case DecisionRecorded, DecisionReversed:
				var d decisionV1
				if err := json.Unmarshal(e.Payload, &d); err != nil {
					return err
				}
				kind := "decision"
				if e.Type == DecisionReversed {
					kind = "reversal"
				}
				if err := q.SetMessageDecision(ctx, pgdb.SetMessageDecisionParams{ID: d.Message, Decision: kind}); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
