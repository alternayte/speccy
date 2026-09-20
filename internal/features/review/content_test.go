package review_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/export"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// Content that is not saved gets every stage with no bundle or version on the server, and a
// second review reuses the pinned questions (SDD §12.2 --server, REQ-047).
func TestReviewContent_NotSaved(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			pe := newPipeline(t, e, map[string]string{}, "fake-large")
			files := []source.File{{Path: "SPEC.md", Content: []byte(groundedSDD)}}
			res, err := pe.reviews.ReviewContent(ctx, review.Content{Slug: "pay", Files: files}, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range res.Findings {
				found = found || f.CheckSlug == review.GroundingContradicted
			}
			if !found || res.ProfileKey != "sdd" || res.Verdict.Result != "not_build_ready" {
				t.Fatalf("result = %+v", res)
			}
			// The second review of the same content reads the pinned questions from the cache.
			again, err := pe.reviews.ReviewContent(ctx, review.Content{Slug: "pay", Files: files}, review.Stages{review.StageDivergence})
			if err != nil {
				t.Fatal(err)
			}
			if again.CacheHits == 0 {
				t.Error("the second review used no cache")
			}
		})
	}
}

// POST /reviews keeps the files and the result, so connected mode links to a report (SDD
// §12.4). A stored review older than 90 days is removed on the next review.
func TestReviewContent_StoredReport(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{})
			db, ws := en.bundles.DB, en.bundles.Workspace
			old := kernel.NewID()
			if err := db.Queries().InsertContentReview(ctx, pgdb.InsertContentReviewParams{ID: old, WorkspaceID: ws, Slug: "old",
				Title: "Old", MainDoc: "SPEC.md", ProfileKey: "sdd", ProfileVersion: 1, Files: dbtype.JSON(`[]`), Result: dbtype.JSON(`{}`),
				CreatedBy: "local", CreatedAt: time.Now().UTC().AddDate(0, 0, -review.ContentReviewDays-1)}); err != nil {
				t.Fatal(err)
			}
			reviews := &review.API{DB: db, Workspace: ws, Service: en.reviews}
			lint := []api.ContentReviewRequestStages{}
			res, err := reviews.ReviewContent(ctx, api.ReviewContentRequestObject{Body: &api.ContentReviewRequest{
				Slug: ptr("pay"), Stages: &lint, Files: []api.ContentFile{{Path: "SPEC.md", Content: groundedSDD}}}})
			if err != nil {
				t.Fatal(err)
			}
			out := res.(api.ReviewContent200JSONResponse)
			if out.ReportPath != "/reviews/"+out.Id.String() {
				t.Errorf("report_path = %q", out.ReportPath)
			}

			ex := &export.API{DB: db, Workspace: ws, Reviews: reviews}
			rep, err := ex.GetContentReviewReport(ctx, api.GetContentReviewReportRequestObject{ReviewId: out.Id})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			if err := rep.VisitGetContentReviewReportResponse(w); err != nil {
				t.Fatal(err)
			}
			body := w.Body.String()
			if !strings.Contains(body, "files not saved on the server") || !bytes.Contains(w.Body.Bytes(), []byte("<article class=\"doc\">")) {
				t.Errorf("the report is missing its parts:\n%s", body[:min(len(body), 600)])
			}
			if len(out.Findings) > 0 && !strings.Contains(body, out.Findings[0].CheckSlug) {
				t.Errorf("the report does not list the finding %s", out.Findings[0].CheckSlug)
			}

			_, err = ex.GetContentReviewReport(ctx, api.GetContentReviewReportRequestObject{ReviewId: old})
			var ke *kernel.Error
			if !errors.As(err, &ke) || ke.Code != "review_not_found" {
				t.Errorf("the review older than 90 days: err = %v", err)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
