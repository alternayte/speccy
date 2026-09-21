package handoff

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// ReportBuild records what a builder learned about the doc (REQ-137). A blocked report opens a
// blocking thread, so a bundle that a builder cannot build stops being Build Ready. A note
// opens an ordinary thread.
func (a *API) ReportBuild(ctx context.Context, req api.ReportBuildRequestObject) (api.ReportBuildResponseObject, error) {
	q := a.DB.Queries()
	h, err := q.GetHandoff(ctx, pgdb.GetHandoffParams{WorkspaceID: a.Workspace, ID: req.HandoffId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("handoff_not_found", "No handoff has this ID. Take the build packet first.")
	}
	if err != nil {
		return nil, err
	}
	b, err := version.Bundle(ctx, q, a.Workspace, h.BundleID)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(req.Body.Text)
	if text == "" {
		return nil, kernel.Invalid("no_text", "Say what you need, so a person can answer it.")
	}
	// A report against a stale handoff never blocks: the doc moved on after the builder took
	// it, so the answer may be in the doc already.
	stale := h.VersionID != b.CurrentVersionID.UUID
	blocking := req.Body.Kind == api.Blocked && !stale
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: h.VersionID})
	number := int64(0)
	if err == nil {
		number = ver.Number
	}

	kind, an, err := a.anchorFor(ctx, b, *req.Body)
	if err != nil {
		return nil, err
	}
	body := text
	if stale && req.Body.Kind == api.Blocked {
		body = text + "\n\nThe builder took version v" + strconv.FormatInt(number, 10) + ". The bundle changed after that, so this does not block the verdict."
	}
	addressed := api.OpenThreadAddressedToHumans
	in := api.OpenThread{
		AnchorKind: kind, Anchor: an, AddressedTo: addressed, Blocking: &blocking, Body: body,
		Title: ptr(title(*req.Body, stale)),
	}
	d, err := a.Threads.OpenFromBuild(ctx, b.ID, h.ID, number, in)
	if err != nil {
		return nil, err
	}
	return api.ReportBuild200JSONResponse(d), nil
}

// title names the thread in the rail, so a person reads what happened before they open it.
func title(r api.BuildReport, stale bool) string {
	what := "A builder is blocked"
	if r.Kind == api.Note || stale {
		what = "A builder left a note"
	}
	switch {
	case r.TraceId != nil && *r.TraceId != "":
		return what + " on " + *r.TraceId
	case r.Section != nil && len(*r.Section) > 0:
		return what + " on " + strings.Join(*r.Section, " › ")
	}
	return what
}

// anchorFor points the thread at the text the report is about: the section it names, where the
// doc defines the trace ID it names, or the doc.
func (a *API) anchorFor(ctx context.Context, b pgdb.Bundle, r api.BuildReport) (api.OpenThreadAnchorKind, map[string]any, error) {
	main, doc, err := a.mainDoc(ctx, b)
	if err != nil {
		return "", nil, err
	}
	if r.TraceId != nil && strings.TrimSpace(*r.TraceId) != "" {
		id := strings.TrimSpace(*r.TraceId)
		for _, d := range lint.Definitions(main, a.prefixes(b)) {
			if strings.EqualFold(d.ID, id) {
				an := anchor.New(b.MainDoc, main, doc, d.Start, d.End)
				return api.OpenThreadAnchorKindText, toMap(an), nil
			}
		}
		return "", nil, kernel.Invalid("trace_id_not_found", "The doc defines no %s. Name a section instead.", id)
	}
	if r.Section != nil && len(*r.Section) > 0 {
		path := *r.Section
		start, end, ok := section.RangeAt(doc, main, path)
		if !ok {
			return "", nil, kernel.Invalid("section_not_found", "The doc has no section %q. Name a heading of the doc, or leave the section out.",
				strings.Join(path, " › "))
		}
		an := anchor.New(b.MainDoc, main, doc, start, end)
		return api.OpenThreadAnchorKindText, toMap(an), nil
	}
	an := anchor.New(b.MainDoc, main, doc, doc.BodyStart, doc.BodyStart)
	return api.OpenThreadAnchorKindText, toMap(an), nil
}

// mainDoc reads and parses the main doc of b's current version.
func (a *API) mainDoc(ctx context.Context, b pgdb.Bundle) ([]byte, section.Doc, error) {
	if !b.CurrentVersionID.Valid {
		return nil, section.Doc{}, kernel.NotFound("no_version", "The bundle has no version.")
	}
	files, err := version.Files(ctx, a.DB.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return nil, section.Doc{}, err
	}
	for _, f := range files {
		if f.Path == b.MainDoc {
			return f.Content, section.Parse(f.Content), nil
		}
	}
	return nil, section.Doc{}, kernel.NotFound("no_main_doc", "The bundle's current version has no main doc.")
}

func toMap(an anchor.Anchor) map[string]any {
	raw, _ := json.Marshal(an)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func ptr[T any](v T) *T { return &v }
