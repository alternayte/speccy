package export

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/render"
)

//go:embed report.html.tmpl
var reportTemplate string

var reportTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"lower": strings.ToLower,
}).Parse(reportTemplate))

type reportFinding struct {
	Level, Check, Message, Fix, Quote, Where string
	Section                                  string
}

type reportData struct {
	Title, Slug, Profile  string
	Version               int64
	Generated             string
	Verdict, VerdictClass string
	Next                  string
	Kind                  string
	Must, Should, Info    int
	Score                 int
	Radar                 []struct {
		Name  string
		Score int
		Has   bool
	}
	Relaxed  int
	Notes    []string
	Findings []reportFinding
	Waived   []reportFinding
	Doc      template.HTML
	MainDoc  string
	Assets   []string
}

// report writes the current version of b as one self-contained HTML file: the verdict, the
// findings, and the rendered main doc with its images inline (REQ-008). It needs no server.
func (a *API) report(ctx context.Context, b pgdb.Bundle) (api.ExportBundleResponseObject, error) {
	q := a.DB.Queries()
	if !b.CurrentVersionID.Valid {
		return nil, kernel.Invalid("no_version", "The bundle has no version to export.")
	}
	v, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: b.CurrentVersionID.UUID})
	if err != nil {
		return nil, err
	}
	files, err := version.Files(ctx, q, v.ID)
	if err != nil {
		return nil, err
	}
	d := reportData{Title: b.Title, Slug: b.Slug, Profile: strings.ToUpper(b.ProfileKey), Version: v.Number,
		Generated: time.Now().UTC().Format("2 January 2006, 15:04 UTC"), MainDoc: b.MainDoc}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Content
		if f.Path != b.MainDoc {
			d.Assets = append(d.Assets, f.Path)
		}
	}

	bv, runErr, err := review.Summary(ctx, q, b)
	if err != nil {
		return nil, err
	}
	switch {
	case bv == nil && runErr != nil:
		d.Verdict, d.VerdictClass, d.Next = "No verdict", "stale", *runErr
	case bv == nil:
		d.Verdict, d.VerdictClass, d.Next = "No verdict", "stale", "Speccy has not reviewed this version."
	default:
		d.Verdict, d.VerdictClass = verdictText(bv), string(bv.Result)
		d.Must, d.Should, d.Info, d.Score, d.Relaxed = bv.Must, bv.Should, bv.Info, bv.Score, bv.RelaxedCount
		d.Kind = "Full review"
		if bv.Kind == api.BundleVerdictKindLint {
			d.Kind = "Lint checks only"
		}
		switch {
		case bv.Result == api.Stale:
			d.Next = fmt.Sprintf("This verdict is for version %d. The report shows version %d.", bv.VersionNumber, v.Number)
		case bv.Must > 0:
			d.Next = fmt.Sprintf("%d MUST finding%s to fix. SHOULD findings never block.", bv.Must, plural(bv.Must))
		case bv.Result == api.NotBuildReady:
			d.Next = "A required link or decision is missing, or a blocking thread is open."
		default:
			d.Next = "No blocking findings."
		}
		for _, c := range verdict.Categories {
			score, has := bv.Radar[string(c)]
			d.Radar = append(d.Radar, struct {
				Name  string
				Score int
				Has   bool
			}{Name: strings.ToUpper(string(c[:1])) + string(c[1:]), Score: score, Has: has})
		}
		run, err := q.GetRun(ctx, pgdb.GetRunParams{WorkspaceID: a.Workspace, ID: bv.RunId})
		if err != nil {
			return nil, err
		}
		_ = jsonStrings(run.Notes, &d.Notes)
		res, err := a.Reviews.ListFindings(ctx, api.ListFindingsRequestObject{RunId: bv.RunId})
		if err != nil {
			return nil, err
		}
		for _, f := range res.(api.ListFindings200JSONResponse).Items {
			rf := reportFinding{Level: string(f.Level), Check: f.CheckSlug, Message: f.Message, Quote: f.Anchor.Quote,
				Section: strings.Join(f.Anchor.HeadingPath, " › "), Where: f.Anchor.File}
			if src, ok := byPath[f.Anchor.File]; ok && f.Anchor.Start <= len(src) && (f.Anchor.Detached == nil || !*f.Anchor.Detached) {
				rf.Where = fmt.Sprintf("%s:%d", f.Anchor.File, bytes.Count(src[:f.Anchor.Start], []byte("\n"))+1)
			}
			if len(rf.Quote) > 400 {
				rf.Quote = rf.Quote[:strings.LastIndexByte(rf.Quote[:400], ' ')+1] + "…"
			}
			if f.Fix != nil {
				rf.Fix = *f.Fix
			}
			if f.Waived {
				d.Waived = append(d.Waived, rf)
			} else {
				d.Findings = append(d.Findings, rf)
			}
		}
		rank := map[string]int{"MUST": 0, "SHOULD": 1, "INFO": 2}
		sort.SliceStable(d.Findings, func(i, j int) bool { return rank[d.Findings[i].Level] < rank[d.Findings[j].Level] })
	}

	// Images become data URIs, so the report opens anywhere.
	doc, err := render.HTML(byPath[b.MainDoc], render.Links{Dir: path.Dir(b.MainDoc), Image: func(p string) string {
		content, ok := byPath[p]
		if !ok {
			return p
		}
		t := mime.TypeByExtension(path.Ext(p))
		if !strings.HasPrefix(t, "image/") {
			return p
		}
		return "data:" + t + ";base64," + base64.StdEncoding.EncodeToString(content)
	}})
	if err != nil {
		return nil, err
	}
	d.Doc = template.HTML(doc) // #nosec G203 -- the renderer escapes doc text and drops raw HTML (SDD §14.3)
	var buf bytes.Buffer
	if err := reportTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	name := path.Base(b.Slug)
	if name == "." || name == "/" {
		name = "bundle"
	}
	return htmlFile{name: fmt.Sprintf("%s-v%d-report.html", name, v.Number), data: buf.Bytes()}, nil
}

func verdictText(v *api.BundleVerdict) string {
	label := map[api.VerdictResult]string{api.BuildReady: "Build Ready", api.NotBuildReady: "Not Build Ready", api.Stale: "Stale"}[v.Result]
	if v.WaiverCount > 0 {
		label += fmt.Sprintf(" (%d waiver%s)", v.WaiverCount, plural(v.WaiverCount))
	}
	return label
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type htmlFile struct {
	name string
	data []byte
}

func (h htmlFile) VisitExportBundleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(h.data)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", h.name))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(h.data)
	return err
}

func jsonStrings(raw []byte, out *[]string) error { return json.Unmarshal(raw, out) }
