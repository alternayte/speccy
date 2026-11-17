package breakdown

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// GetBreakdown returns the breakdown of a doc.
func (a *API) GetBreakdown(ctx context.Context, req api.GetBreakdownRequestObject) (api.GetBreakdownResponseObject, error) {
	v, err := a.view(ctx, a.DB.Queries(), req.DocId)
	if err != nil {
		return nil, err
	}
	return api.GetBreakdown200JSONResponse(a.ToAPI(ctx, v)), nil
}

// ProposeBreakdown proposes a new breakdown of the doc's current version.
func (a *API) ProposeBreakdown(ctx context.Context, req api.ProposeBreakdownRequestObject) (api.ProposeBreakdownResponseObject, error) {
	ack := req.Body != nil && req.Body.Acknowledged != nil && *req.Body.Acknowledged
	if _, err := a.Propose(ctx, req.DocId, ack); err != nil {
		return nil, err
	}
	v, err := a.view(ctx, a.DB.Queries(), req.DocId)
	if err != nil {
		return nil, err
	}
	return api.ProposeBreakdown201JSONResponse(a.ToAPI(ctx, v)), nil
}

// EditBreakdown applies edits.
func (a *API) EditBreakdown(ctx context.Context, req api.EditBreakdownRequestObject) (api.EditBreakdownResponseObject, error) {
	ops, err := opsOf(req.Body.Ops)
	if err != nil {
		return nil, err
	}
	if req.Body.BasedOn <= 0 {
		return nil, kernel.Invalid("no_based_on", "Pass based_on: the revision of the breakdown you read.")
	}
	if _, err := a.Edit(ctx, req.DocId, req.Body.BasedOn, ops); err != nil {
		return nil, err
	}
	v, err := a.view(ctx, a.DB.Queries(), req.DocId)
	if err != nil {
		return nil, err
	}
	return api.EditBreakdown200JSONResponse(a.ToAPI(ctx, v)), nil
}

// opsOf reads the API ops. The field names of an op are the same in the API and in Op.
func opsOf(in []api.BreakdownOp) ([]Op, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out []Op
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&out); err != nil {
		return nil, kernel.Invalid("bad_op", "The changes do not read: %s.", err.Error())
	}
	return out, nil
}

// UpdateBreakdown moves a stale breakdown to the doc's current version.
func (a *API) UpdateBreakdown(ctx context.Context, req api.UpdateBreakdownRequestObject) (api.UpdateBreakdownResponseObject, error) {
	ack := req.Body.Acknowledged != nil && *req.Body.Acknowledged
	if _, err := a.UpdateToVersion(ctx, req.DocId, req.Body.BasedOn, ack); err != nil {
		return nil, err
	}
	v, err := a.view(ctx, a.DB.Queries(), req.DocId)
	if err != nil {
		return nil, err
	}
	return api.UpdateBreakdown200JSONResponse(a.ToAPI(ctx, v)), nil
}

// CheckBreakdownReaders runs the reader check on one item.
func (a *API) CheckBreakdownReaders(ctx context.Context, req api.CheckBreakdownReadersRequestObject) (api.CheckBreakdownReadersResponseObject, error) {
	if _, err := a.CheckReaders(ctx, req.DocId, req.Body.Item, 0); err != nil {
		return nil, err
	}
	v, err := a.view(ctx, a.DB.Queries(), req.DocId)
	if err != nil {
		return nil, err
	}
	return api.CheckBreakdownReaders200JSONResponse(a.ToAPI(ctx, v)), nil
}

// ListBreakdownTrackers lists the trackers a push can write to.
func (a *API) ListBreakdownTrackers(ctx context.Context, req api.ListBreakdownTrackersRequestObject) (api.ListBreakdownTrackersResponseObject, error) {
	if _, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, req.DocId); err != nil {
		return nil, err
	}
	out := api.ListBreakdownTrackers200JSONResponse{Items: []api.Tracker{}}
	linear := api.Tracker{Kind: api.TrackerKindLinear, Name: "Linear", Ready: false}
	if a.Connection != nil {
		if _, ok, err := a.Connection(ctx, LinearHost); err == nil && ok {
			linear.Ready = true
		}
	}
	if !linear.Ready {
		r := "No MCP connection lists the host linear.app."
		linear.Reason = &r
	}
	out.Items = append(out.Items, linear)
	if a.Hosts != nil {
		hosts, err := a.Hosts(ctx)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			if h == LinearHost || h == "github.com" || strings.HasSuffix(h, ".github.com") {
				continue
			}
			host := h
			out.Items = append(out.Items, api.Tracker{Kind: api.TrackerKindJira, Name: "Jira at " + h, Host: &host, Ready: true})
		}
	}
	gh := api.Tracker{Kind: api.TrackerKindGithub, Name: "GitHub Issues", Ready: a.GitHub != nil}
	if !gh.Ready {
		r := "Speccy has no GitHub credential."
		gh.Reason = &r
	}
	out.Items = append(out.Items, gh)
	return out, nil
}

// ListBreakdownAssignees lists the people and the agents of a tracker.
func (a *API) ListBreakdownAssignees(ctx context.Context, req api.ListBreakdownAssigneesRequestObject) (api.ListBreakdownAssigneesResponseObject, error) {
	if _, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, req.DocId); err != nil {
		return nil, err
	}
	tr, err := a.openTracker(ctx, req.Params.Tracker, req.Params.Target)
	if err != nil {
		return nil, err
	}
	defer tr.close()
	if err := tr.check(ctx, false); err != nil {
		return nil, err
	}
	query := ""
	if req.Params.Query != nil {
		query = *req.Params.Query
	}
	list, err := tr.assignees(ctx, query)
	if err != nil {
		return nil, err
	}
	out := api.ListBreakdownAssignees200JSONResponse{Items: []api.Assignee{}}
	// Agents are listed apart from people: first, so a builder agent is one click away.
	for _, kind := range []string{"agent", "person"} {
		for _, as := range list {
			if as.Kind == kind {
				out.Items = append(out.Items, *assigneeAPI(&as))
			}
		}
	}
	return out, nil
}

// PushBreakdown creates the unpushed items as tracker issues.
func (a *API) PushBreakdown(ctx context.Context, req api.PushBreakdownRequestObject) (api.PushBreakdownResponseObject, error) {
	ack := req.Body.Acknowledged != nil && *req.Body.Acknowledged
	res, err := a.Push(ctx, req.DocId, req.Body.BasedOn, string(req.Body.Tracker), req.Body.Target, ack)
	if err != nil {
		return nil, err
	}
	out := api.PushBreakdown200JSONResponse{HandoffId: res.Handoff, Created: []api.CreatedIssue{},
		Breakdown: a.ToAPI(ctx, res.View)}
	for _, c := range res.Created {
		out.Created = append(out.Created, api.CreatedIssue{Kind: api.CreatedIssueKind(c.Kind), Id: c.ID, Title: c.Title, Key: c.Key, Url: c.URL})
	}
	return out, nil
}

// ExportBreakdown writes the breakdown as markdown or CSV.
func (a *API) ExportBreakdown(ctx context.Context, req api.ExportBreakdownRequestObject) (api.ExportBreakdownResponseObject, error) {
	raw, err := a.Export(ctx, req.DocId, req.Params.Format)
	if err != nil {
		return nil, err
	}
	if req.Params.Format == "csv" {
		return api.ExportBreakdown200TextcsvResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
	}
	return api.ExportBreakdown200TextmarkdownResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
}

// GetWorkItem returns the slice of the spec one work item builds.
func (a *API) GetWorkItem(ctx context.Context, req api.GetWorkItemRequestObject) (api.GetWorkItemResponseObject, error) {
	out, err := a.WorkItem(ctx, req.Params.Ref)
	if err != nil {
		return nil, err
	}
	return api.GetWorkItem200JSONResponse(out), nil
}
