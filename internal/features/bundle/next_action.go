package bundle

import (
	"context"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/nextaction"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Deps are what the next action needs from the other features. The bundle API holds them as
// functions, so this package depends on none of them.
type Deps struct {
	// Waiting returns the waivers that wait for the caller across the workspace (SDD §9.1).
	Waiting func(ctx context.Context) ([]api.Waiver, error)
	// Questions returns the list of the questions screen of a doc.
	Questions func(ctx context.Context, docID uuid.UUID) (*api.Questions, error)
	// Approvals returns how many approvals the profile needs, and how many the bundle has.
	Approvals func(ctx context.Context, bundleID uuid.UUID) (needed, given int, err error)
	// Handoffs returns how many builders took the packet of a bundle version.
	Handoffs func(ctx context.Context, bundleID uuid.UUID, version int64) (int, error)
}

// brief names the next action from the signals a list already reads: the verdict, the waivers
// that wait for this person, and the status. It carries no target.
func (a *API) brief(ctx context.Context, q store.Querier, b pgdb.SpecDoc, out *api.SpecDoc, waiting []api.Waiver) error {
	canEdit, err := share.CanEdit(ctx, q, kernel.ActorFrom(ctx), b)
	if err != nil {
		return err
	}
	in := nextaction.Inputs{
		WaiverWaiting:  nextaction.WaiverFor(waiting, b.ID),
		Verdict:        out.Verdict,
		CurrentVersion: out.CurrentVersion.Number,
		RunError:       out.RunError,
		CanEdit:        canEdit,
		Hosted:         a.Service.Local == nil,
	}
	if out.Status != nil {
		in.Status = *out.Status
	}
	in.Questions, in.Recheck = nextaction.ListQuestions(out.Verdict)
	if in.Hosted && a.Deps.Approvals != nil && in.Verdict != nil && in.Verdict.Result == api.VerdictResultBuildReady {
		needed, given, err := a.Deps.Approvals(ctx, b.ID)
		if err != nil {
			return err
		}
		in.ApprovalsNeeded, in.ApprovalsGiven = needed, given
	}
	out.NextAction = nextaction.Of(in)
	return nil
}

// full names the next action of one bundle. It adds what the list is too big to read: the items
// of the questions screen, and the handoffs.
func (a *API) full(ctx context.Context, q store.Querier, b pgdb.SpecDoc, out *api.SpecDoc) error {
	canEdit, err := share.CanEdit(ctx, q, kernel.ActorFrom(ctx), b)
	if err != nil {
		return err
	}
	in := nextaction.Inputs{
		Verdict:        out.Verdict,
		CurrentVersion: out.CurrentVersion.Number,
		RunError:       out.RunError,
		Adopt:          out.Adopt,
		CanEdit:        canEdit,
		Hosted:         a.Service.Local == nil,
	}
	if out.Status != nil {
		in.Status = *out.Status
	}
	if a.Deps.Waiting != nil {
		waiting, err := a.Deps.Waiting(ctx)
		if err != nil {
			return err
		}
		in.WaiverWaiting = nextaction.WaiverFor(waiting, b.ID)
	}
	if canEdit && a.Deps.Questions != nil && out.Verdict != nil {
		qs, err := a.Deps.Questions(ctx, b.ID)
		if err != nil {
			return err
		}
		in.Questions, in.Recheck = len(qs.Items), qs.Recheck
	}
	if canEdit && out.Verdict != nil && out.Verdict.Result == api.VerdictResultBuildReady {
		if in.Hosted && a.Deps.Approvals != nil {
			needed, given, err := a.Deps.Approvals(ctx, b.ID)
			if err != nil {
				return err
			}
			in.ApprovalsNeeded, in.ApprovalsGiven = needed, given
		}
		if a.Deps.Handoffs != nil {
			n, err := a.Deps.Handoffs(ctx, b.ID, out.CurrentVersion.Number)
			if err != nil {
				return err
			}
			in.Handoffs = n
		}
	}
	out.NextAction = nextaction.Of(in)
	return nil
}
