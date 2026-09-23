// Package handoff gives a coding agent what it needs to build one bundle (REQ-136). Speccy
// never runs the build: it serves the build packet, and it records which version a builder
// took.
package handoff

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the handoff endpoints.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	// Reviews resolves the bundle's links and its build questions.
	Reviews *review.Service
	// Questions lists a run's build questions with their answers.
	Questions *review.API
	People    kernel.Directory
	// Threads opens the thread a build report becomes (REQ-137).
	Threads *thread.API
}

// LinksDir is the folder the packet writes the linked docs in.
const LinksDir = "links"

// TakeHandoff returns the build packet and records the handoff (REQ-136).
func (a *API) TakeHandoff(ctx context.Context, req api.TakeHandoffRequestObject) (api.TakeHandoffResponseObject, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	ack, label := false, ""
	if req.Body != nil {
		if req.Body.Acknowledged != nil {
			ack = *req.Body.Acknowledged
		}
		if req.Body.Label != nil {
			label = strings.TrimSpace(*req.Body.Label)
		}
	}
	v, _, err := review.Summary(ctx, q, b)
	if err != nil {
		return nil, err
	}
	result := verdictOf(v)
	if err := allow(result, v, ack); err != nil {
		return nil, err
	}
	packet, err := a.packet(ctx, b)
	if err != nil {
		return nil, err
	}
	row := pgdb.Handoff{
		ID: kernel.NewID(), WorkspaceID: a.Workspace, SpecDocID: b.ID, VersionID: b.CurrentVersionID.UUID,
		Verdict: result, Acknowledged: ack, Label: label, TakenBy: kernel.ActorFrom(ctx).UserID,
		CreatedAt: time.Now().UTC(),
	}
	if err := q.InsertHandoff(ctx, pgdb.InsertHandoffParams(row)); err != nil {
		return nil, err
	}
	// The re-entry prompt quotes the handoff ID, so it is written after the ID exists.
	packet.HandoffId = row.ID
	packet.HandoffMd = HandoffMarkdown(packet)
	return api.TakeHandoff200JSONResponse(packet), nil
}

// verdictOf names the bundle's verdict, or "none" when it has no run.
func verdictOf(v *api.BundleVerdict) string {
	if v == nil {
		return "none"
	}
	return string(v.Result)
}

// allow refuses a handoff that the verdict does not allow. The verdict is the only lever
// Speccy has, so it bites first; acknowledged takes the packet anyway.
func allow(result string, v *api.BundleVerdict, ack bool) error {
	if result == string(verdict.BuildReady) || ack {
		return nil
	}
	switch result {
	case "none":
		return kernel.Conflict("no_verdict", "This bundle has no review, so Speccy cannot say it is Build Ready. Run a review, or take it anyway with acknowledged.")
	case string(verdict.Stale):
		return kernel.Conflict("stale_verdict", "The verdict is stale: it belongs to an older version. Run the review again, or take it anyway with acknowledged.")
	}
	must := 0
	if v != nil {
		must = v.Must
	}
	return kernel.Conflict("not_build_ready", "This bundle is Not Build Ready: %d MUST finding%s to fix. Fix them, or take it anyway with acknowledged.",
		must, plural(must))
}

// packet builds the build packet of b's current version.
func (a *API) packet(ctx context.Context, b pgdb.SpecDoc) (api.BuildPacket, error) {
	q := a.DB.Queries()
	if !b.CurrentVersionID.Valid {
		return api.BuildPacket{}, kernel.NotFound("no_version", "The bundle has no version.")
	}
	ver, err := version.Get(ctx, q, b, nil)
	if err != nil {
		return api.BuildPacket{}, err
	}
	files, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
	if err != nil {
		return api.BuildPacket{}, err
	}
	out := api.BuildPacket{
		Bundle: b.Slug, Title: b.Title, VersionNumber: ver.Number, MainDoc: b.DocPath,
		Files: []api.ContentFile{}, Links: []api.PacketLink{}, ExternalLinks: []api.PacketExternalLink{},
		TraceIds:  []api.PacketTraceId{},
		Questions: []api.PacketQuestion{},
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			main = f.Content
		}
		out.Files = append(out.Files, contentFile(f.Path, f.Content))
	}
	if main == nil {
		return api.BuildPacket{}, kernel.NotFound("no_main_doc", "The bundle's current version has no main doc.")
	}
	if out.Links, err = a.links(ctx, b, main); err != nil {
		return api.BuildPacket{}, err
	}
	if out.ExternalLinks, err = a.externalLinks(ctx, b); err != nil {
		return api.BuildPacket{}, err
	}
	out.TraceIds = traceIDs(main, a.prefixes(b))
	if out.Questions, err = a.questions(ctx, b); err != nil {
		return api.BuildPacket{}, err
	}
	return out, nil
}

// prefixes are the trace ID prefixes of the bundle's profile.
func (a *API) prefixes(b pgdb.SpecDoc) []string {
	if p, ok := a.Profiles()[b.ProfileKey]; ok {
		return p.Profile.Trace.Prefixes
	}
	return nil
}

// links carries the main doc of each bundle this one links to, so the builder reads the
// upstream text without a second call.
func (a *API) links(ctx context.Context, b pgdb.SpecDoc, main []byte) ([]api.PacketLink, error) {
	out := []api.PacketLink{}
	linked, err := a.Reviews.LinkedBundles(ctx, b, main)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	for _, l := range linked {
		if seen[l.Bundle.ID] || !l.Bundle.CurrentVersionID.Valid {
			continue
		}
		seen[l.Bundle.ID] = true
		files, err := version.Files(ctx, a.DB.Queries(), l.Bundle.CurrentVersionID.UUID)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.Path != l.Bundle.DocPath {
				continue
			}
			out = append(out, api.PacketLink{
				Kind: l.Kind, Bundle: l.Bundle.Slug, Title: l.Bundle.Title,
				Path: path.Join(LinksDir, path.Base(l.Bundle.Slug)+path.Ext(l.Bundle.DocPath)), Content: string(f.Content),
			})
		}
	}
	return out, nil
}

// externalLinks carries the issues, pages, and code the doc links to: the kind, the URL, and
// for a code target the commit the last run read. Speccy fetches no content (DEC-021).
func (a *API) externalLinks(ctx context.Context, b pgdb.SpecDoc) ([]api.PacketExternalLink, error) {
	q := a.DB.Queries()
	rows, err := q.ListLinksFrom(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	states, err := q.ListLinkStates(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	commit := map[string]string{}
	for _, st := range states {
		commit[st.TargetRef] = st.CheckedRef
	}
	out := []api.PacketExternalLink{}
	for _, l := range rows {
		if l.TargetKind != "external" || l.TargetUrl == "" {
			continue
		}
		e := api.PacketExternalLink{Kind: l.Kind, Ref: l.TargetRef, Url: l.TargetUrl}
		if c := commit[l.TargetRef]; c != "" {
			e.Commit = &c
		}
		out = append(out, e)
	}
	return out, nil
}

// traceIDs are the doc's own trace IDs, in document order. They are the units of work the
// re-entry prompt lists.
func traceIDs(main []byte, prefixes []string) []api.PacketTraceId {
	out := []api.PacketTraceId{}
	for _, d := range lint.Definitions(main, prefixes) {
		out = append(out, api.PacketTraceId{Id: d.ID, Text: strings.TrimSpace(d.Text)})
	}
	return out
}

// questions are the build questions of the bundle's newest full run, with the answer the
// readers agreed on. A question they read differently carries no answer.
func (a *API) questions(ctx context.Context, b pgdb.SpecDoc) ([]api.PacketQuestion, error) {
	out := []api.PacketQuestion{}
	if a.Questions == nil {
		return out, nil
	}
	v, _, err := review.Summary(ctx, a.DB.Queries(), b)
	if err != nil || v == nil || v.Kind != api.BundleVerdictKindFull {
		return out, err
	}
	res, err := a.Questions.ListQuestions(ctx, api.ListQuestionsRequestObject{RunId: v.RunId})
	if err != nil {
		return out, err
	}
	list, ok := res.(api.ListQuestions200JSONResponse)
	if !ok {
		return out, nil
	}
	for _, q := range list.Items {
		pq := api.PacketQuestion{Number: q.Number, Text: q.Text, Result: api.PacketQuestionResult(q.Result)}
		if q.Result == api.BuildQuestionResultAgree {
			for _, ans := range q.Answers {
				if ans.Answered && strings.TrimSpace(ans.Answer) != "" {
					answer := strings.TrimSpace(ans.Answer)
					pq.Answer = &answer
					break
				}
			}
		}
		out = append(out, pq)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// ListHandoffs lists the handoffs of a bundle, newest first (REQ-136).
func (a *API) ListHandoffs(ctx context.Context, req api.ListHandoffsRequestObject) (api.ListHandoffsResponseObject, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListHandoffs(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := api.ListHandoffs200JSONResponse{Items: []api.Handoff{}}
	for _, r := range rows {
		v, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: r.VersionID})
		number := int64(0)
		if err == nil {
			number = v.Number
		}
		item := api.Handoff{
			Id: r.ID, VersionNumber: number, Verdict: r.Verdict, Acknowledged: r.Acknowledged,
			TakenBy: kernel.PersonByID(ctx, a.People, r.TakenBy).Label(),
			Stale:   r.VersionID != b.CurrentVersionID.UUID, CreatedAt: r.CreatedAt.UTC(),
		}
		if r.Label != "" {
			item.Label = &r.Label
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func contentFile(path string, content []byte) api.ContentFile {
	if isText(content) {
		return api.ContentFile{Path: path, Content: string(content)}
	}
	enc := api.ContentFileEncodingBase64
	return api.ContentFile{Path: path, Content: base64Of(content), Encoding: &enc}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// CountForVersion returns how many builders took the packet of one bundle version. The next
// action reads it (SDD §13.4).
func (a *API) CountForVersion(ctx context.Context, bundleID uuid.UUID, number int64) (int, error) {
	rows, err := a.DB.Queries().ListHandoffs(ctx, bundleID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		v, err := a.DB.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: bundleID, ID: r.VersionID})
		if err == nil && v.Number == number {
			n++
		}
	}
	return n, nil
}
