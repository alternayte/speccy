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
	// FirstPoint returns the first point of a bundle's tour, or nil.
	FirstPoint func(ctx context.Context, bundleID uuid.UUID) (*api.TourPoint, error)
	// FirstMust returns the first MUST finding of a run that no waiver covers, or nil.
	FirstMust func(ctx context.Context, runID uuid.UUID) (*api.Finding, error)
	// Approvals returns how many approvals the profile needs, and how many the bundle has.
	Approvals func(ctx context.Context, bundleID uuid.UUID) (needed, given int, err error)
	// Handoffs returns how many builders took the packet of a bundle version.
	Handoffs func(ctx context.Context, bundleID uuid.UUID, version int64) (int, error)
}

// brief names the next action from the signals a list already reads: the verdict, the waivers
// that wait for this person, and the status. It carries no target.
func (a *API) brief(ctx context.Context, q store.Querier, b pgdb.Bundle, out *api.Bundle, waiting []api.Waiver) error {
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
	if in.Hosted && a.Deps.Approvals != nil && in.Verdict != nil && in.Verdict.Result == api.BuildReady {
		needed, given, err := a.Deps.Approvals(ctx, b.ID)
		if err != nil {
			return err
		}
		in.ApprovalsNeeded, in.ApprovalsGiven = needed, given
	}
	out.NextAction = nextaction.Of(in)
	return nil
}

// full names the next action of one bundle. It adds what the list is too big to read: the tour
// point, the first MUST finding, and the handoffs, so the action carries a target.
func (a *API) full(ctx context.Context, q store.Querier, b pgdb.Bundle, out *api.Bundle) error {
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
	if canEdit && a.Deps.FirstPoint != nil {
		p, err := a.Deps.FirstPoint(ctx, b.ID)
		if err != nil {
			return err
		}
		in.Point = p
	}
	if canEdit && in.Point == nil && a.Deps.FirstMust != nil && out.Verdict != nil {
		f, err := a.Deps.FirstMust(ctx, out.Verdict.RunId)
		if err != nil {
			return err
		}
		in.Finding = f
	}
	if canEdit && out.Verdict != nil && out.Verdict.Result == api.BuildReady {
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
