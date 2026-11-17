package breakdown

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Ref is one work item, resolved: the doc and the item ID, and the pushed issue when there is
// one.
type Ref struct {
	DocID uuid.UUID
	Item  string
	Issue *pgdb.BreakdownIssue
}

// Resolve finds the work item a reference names: its Speccy URL, its tracker issue URL or key,
// or bundle/item.
func (a *API) Resolve(ctx context.Context, ref string) (Ref, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Ref{}, kernel.Invalid("no_ref", "Name the work item: its issue URL or key, its Speccy URL, or bundle/item.")
	}
	q := a.DB.Queries()
	for _, try := range []string{ref, strings.TrimSuffix(ref, "/")} {
		rows, err := q.FindBreakdownIssue(ctx, pgdb.FindBreakdownIssueParams{WorkspaceID: a.Workspace, Ref: try})
		if err != nil {
			return Ref{}, err
		}
		for _, r := range rows {
			if r.Kind == "item" {
				r := r
				return Ref{DocID: r.SpecDocID, Item: r.ItemID, Issue: &r}, nil
			}
		}
	}
	// A Speccy URL: /docs/<id>/breakdown?item=<item>.
	if u, err := url.Parse(ref); err == nil && u.Scheme != "" {
		parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		for i := 0; i+2 < len(parts); i++ {
			if parts[i] == "docs" && parts[i+2] == "breakdown" {
				if id, err := uuid.Parse(parts[i+1]); err == nil && u.Query().Get("item") != "" {
					return a.withIssue(ctx, q, id, u.Query().Get("item"))
				}
			}
		}
		return Ref{}, kernel.NotFound("no_work_item", "No pushed work item has the URL %s.", ref)
	}
	// bundle/item. A bundle slug can hold a slash, so the item is the last part.
	slug, item := path.Dir(ref), path.Base(ref)
	if slug == "." || item == "" {
		return Ref{}, kernel.NotFound("no_work_item", "No pushed work item has the key %s. Name bundle/item for one that is not pushed.", ref)
	}
	b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: a.Workspace, Slug: slug})
	if errors.Is(err, sql.ErrNoRows) {
		if id, perr := uuid.Parse(slug); perr == nil {
			return a.withIssue(ctx, q, id, item)
		}
		return Ref{}, kernel.NotFound("no_work_item", "No bundle has the slug %s.", slug)
	}
	if err != nil {
		return Ref{}, err
	}
	return a.withIssue(ctx, q, b.ID, item)
}

func (a *API) withIssue(ctx context.Context, q store.Querier, docID uuid.UUID, item string) (Ref, error) {
	s, ok, err := a.load(ctx, q, docID)
	if err != nil {
		return Ref{}, err
	}
	if !ok {
		return Ref{}, noBreakdown()
	}
	if s.Body.item(item) == nil {
		return Ref{}, kernel.NotFound("no_work_item", "The breakdown has no item %s.", item)
	}
	out := Ref{DocID: docID, Item: item}
	rows, err := q.ListBreakdownIssues(ctx, s.Row.ID)
	if err != nil {
		return Ref{}, err
	}
	for _, r := range rows {
		if r.Kind == "item" && r.ItemID == item {
			r := r
			out.Issue = &r
		}
	}
	return out, nil
}

// TraceIDs returns the trace IDs of the work item a reference names, for a scoped verification.
func (a *API) TraceIDs(ctx context.Context, ref string) (docID uuid.UUID, ids []string, name string, err error) {
	r, err := a.Resolve(ctx, ref)
	if err != nil {
		return uuid.Nil, nil, "", err
	}
	s, _, err := a.load(ctx, a.DB.Queries(), r.DocID)
	if err != nil {
		return uuid.Nil, nil, "", err
	}
	it := s.Body.item(r.Item)
	b, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, r.DocID)
	if err != nil {
		return uuid.Nil, nil, "", err
	}
	if len(it.TraceIDs) == 0 {
		return uuid.Nil, nil, "", kernel.Invalid("no_trace_ids",
			"The work item %s/%s has no trace ID, so a verification run cannot scope to it.", b.Slug, it.ID)
	}
	return r.DocID, it.TraceIDs, b.Slug + "/" + it.ID, nil
}

// WorkItem returns the slice of the spec one work item builds: the item, and the doc at the
// version it was pushed from. It says when the doc has a newer version.
func (a *API) WorkItem(ctx context.Context, ref string) (api.WorkItemPacket, error) {
	r, err := a.Resolve(ctx, ref)
	if err != nil {
		return api.WorkItemPacket{}, err
	}
	q := a.DB.Queries()
	v, err := a.view(ctx, q, r.DocID)
	if err != nil {
		return api.WorkItemPacket{}, err
	}
	it := v.Body.item(r.Item)
	pinned := v.Row.VersionID
	out := api.WorkItemPacket{Bundle: v.Bundle.Slug, DocId: v.Bundle.ID, Item: a.itemAPI(ctx, v, *it),
		VersionNumber: v.VersionNumber, CurrentVersionNumber: v.CurrentVersion, MainDoc: v.Bundle.DocPath,
		Links: []api.PacketLink{}, ExternalLinks: []api.PacketExternalLink{}}
	if r.Issue != nil {
		pinned = r.Issue.VersionID
		out.VersionNumber = v.Versions[pinned]
		id := r.Issue.HandoffID
		out.HandoffId = &id
		if h, err := q.GetHandoff(ctx, pgdb.GetHandoffParams{WorkspaceID: a.Workspace, ID: id}); err == nil {
			out.Verdict = &h.Verdict
		}
	}
	out.NewerVersion = pinned != v.Bundle.CurrentVersionID.UUID
	files, err := version.Files(ctx, q, pinned)
	if err != nil {
		return api.WorkItemPacket{}, err
	}
	for _, f := range files {
		if f.Path == v.Bundle.DocPath {
			out.Doc = string(f.Content)
		}
	}
	d, err := a.read(ctx, q, v.Bundle, true)
	if err != nil {
		return api.WorkItemPacket{}, err
	}
	for _, l := range d.linked {
		out.Links = append(out.Links, api.PacketLink{Kind: l.Kind, Bundle: l.Slug, Title: l.Title,
			Path: path.Join("links", path.Base(l.Slug)+".md"), Content: string(l.Text)})
	}
	links, err := q.ListLinksFrom(ctx, v.Bundle.ID)
	if err != nil {
		return api.WorkItemPacket{}, err
	}
	for _, l := range links {
		if l.TargetKind == "external" && l.TargetUrl != "" {
			out.ExternalLinks = append(out.ExternalLinks, api.PacketExternalLink{Kind: l.Kind, Ref: l.TargetRef, Url: l.TargetUrl})
		}
	}
	return out, nil
}
