package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// DeleteBundlePlan returns what the Delete control offers for this bundle.
func (a *API) DeleteBundlePlan(ctx context.Context, req api.DeleteBundlePlanRequestObject) (api.DeleteBundlePlanResponseObject, error) {
	s := a.Service
	b, err := version.Bundle(ctx, s.DB.Queries(), s.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	out := api.DeletePlan{Kind: api.DeletePlanKind(b.SourceKind), Slug: b.Slug}
	switch b.SourceKind {
	case KindDB:
		out.Message = "This bundle and everything about it goes: its versions, its reviews, its threads, its waivers, its handoffs and its verification runs."
	case KindGitHub:
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		src, err := s.DB.Queries().GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: ref.Source})
		if err != nil {
			return nil, err
		}
		n, err := s.bundlesOfSource(ctx, ref.Source)
		if err != nil {
			return nil, err
		}
		id := src.ID
		out.SourceId, out.SourceRepo, out.SourceBundles = &id, &src.Repo, &n
		out.Message = fmt.Sprintf("%s makes this bundle from %s, so the next sync makes it again. Remove the source to be rid of it. %d bundle%s go with it.",
			src.Repo, src.Path, n, plural(n))
	default:
		dir := b.Slug
		out.Dir = &dir
		out.Message = "The folder on disk is the truth for this bundle. Take " + dir + " out of the served folder, and the next scan drops it."
	}
	return api.DeleteBundlePlan200JSONResponse(out), nil
}

// DeleteBundle removes a bundle whose text Speccy holds, and everything that hangs off it.
func (a *API) DeleteBundle(ctx context.Context, req api.DeleteBundleRequestObject) (api.DeleteBundleResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	b, err := version.Bundle(ctx, q, s.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	if b.SourceKind != KindDB {
		return nil, kernel.Invalid("not_deletable",
			"A %s bundle is made from somewhere else, so removing the record alone is a lie. Open Delete on the bundle to see what removes it.", b.SourceKind)
	}
	// Who may act comes before what they typed, so a person who may not delete never learns
	// whether their confirmation was right.
	if err := a.mayDelete(ctx, b); err != nil {
		return nil, err
	}
	if req.Params.Slug != b.Slug {
		return nil, kernel.Invalid("slug_mismatch", "Type the bundle's slug, %s, to confirm.", b.Slug)
	}
	if err := s.DeleteBundleData(ctx, b.ID); err != nil {
		return nil, err
	}
	return api.DeleteBundle204Response{}, nil
}

// mayDelete allows an author of the bundle or an admin. A guest never deletes.
func (a *API) mayDelete(ctx context.Context, b pgdb.Bundle) error {
	act := kernel.ActorFrom(ctx)
	if act.Guest != nil {
		return kernel.Forbidden("not_an_author", "A guest cannot delete a bundle.")
	}
	if act.Role == kernel.RoleAdmin {
		return nil
	}
	authors, err := a.Service.DB.Queries().ListBundleAuthors(ctx, b.ID)
	if err != nil {
		return err
	}
	for _, id := range authors {
		if id == act.UserID {
			return nil
		}
	}
	return kernel.Forbidden("not_an_author", "An author of this bundle or an admin deletes it.")
}

// DeleteBundleData removes the bundle and everything that hangs off it, in one transaction.
// The order is children first, because the foreign keys do not cascade. A blob stays while
// another version still names it: blobs are shared by content.
func (s *Service) DeleteBundleData(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		threads, err := q.ThreadIDsOfBundle(ctx, uuid.NullUUID{UUID: id, Valid: true})
		if err != nil {
			return err
		}
		waivers, err := q.WaiverIDsOfBundle(ctx, id)
		if err != nil {
			return err
		}
		for _, step := range []func(context.Context, uuid.UUID) error{
			q.DeleteVerificationOutcomesOfBundle,
			q.DeleteVerificationRunsOfBundle,
			q.DeleteHandoffsOfBundle,
			q.DeleteVerdictsOfBundle,
			q.DeleteAnswersOfBundle,
			q.DeleteQuestionResultsOfBundle,
			q.DeleteQuestionsOfBundle,
			q.DeleteFindingsOfBundle,
			q.DeleteClaimsOfBundle,
			q.DeleteRunLinksOfBundle,
			q.DeleteReviewRunsOfBundle,
			q.DeleteLinkStatesOfBundle,
			q.DeleteWaiversOfBundle,
			q.DeleteBundleStatusView,
			q.DeleteBundleReviewers,
			q.DeleteBundleAuthors,
			q.DeleteVersionFilesOfBundle,
		} {
			if err := step(ctx, id); err != nil {
				return err
			}
		}
		if err := q.DeleteLinksOfBundle(ctx, id); err != nil {
			return err
		}
		nb := uuid.NullUUID{UUID: id, Valid: true}
		if err := q.DeleteThreadMessagesOfBundle(ctx, nb); err != nil {
			return err
		}
		if err := q.DeleteThreadsOfBundle(ctx, nb); err != nil {
			return err
		}
		if err := q.ClearBundleHead(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteVersionsOfBundle(ctx, id); err != nil {
			return err
		}
		// The event streams behind the threads, the waivers and the bundle status.
		streams := append([]uuid.UUID{id}, threads...)
		streams = append(streams, waivers...)
		for _, sid := range streams {
			if err := q.DeleteEventsOfStream(ctx, sid); err != nil {
				return err
			}
			if err := q.DeleteStream(ctx, sid); err != nil {
				return err
			}
		}
		if err := q.DeleteBundleRow(ctx, pgdb.DeleteBundleRowParams{WorkspaceID: s.Workspace, ID: id}); err != nil {
			return err
		}
		// A blob with no version_file left is content nothing names any more.
		return q.DeleteOrphanBlobs(ctx)
	})
}

// bundlesOfSource counts the bundles a GitHub source holds.
func (s *Service) bundlesOfSource(ctx context.Context, src uuid.UUID) (int, error) {
	rows, err := s.DB.Queries().ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{
		WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range rows {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == src && !b.ArchivedAt.Valid {
			n++
		}
	}
	return n, nil
}
