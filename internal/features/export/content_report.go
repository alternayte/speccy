package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// GetContentReviewReport is the HTML report of a review from POST /reviews (SDD §12.4): the
// connected-mode Action links to it.
func (a *API) GetContentReviewReport(ctx context.Context, req api.GetContentReviewReportRequestObject) (api.GetContentReviewReportResponseObject, error) {
	row, err := a.DB.Queries().GetContentReview(ctx, pgdb.GetContentReviewParams{WorkspaceID: a.Workspace, ID: req.ReviewId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("review_not_found", "The review does not exist, or it is older than %d days.", review.ContentReviewDays)
	}
	if err != nil {
		return nil, err
	}
	var res api.ContentReview
	if err := json.Unmarshal(row.Result, &res); err != nil {
		return nil, err
	}
	var files []review.StoredFile
	if err := json.Unmarshal(row.Files, &files); err != nil {
		return nil, err
	}
	d := reportData{Title: row.Title, Slug: row.Slug, Profile: strings.ToUpper(row.ProfileKey), MainDoc: row.MainDoc,
		Generated: row.CreatedAt.UTC().Format("2 January 2006, 15:04 UTC"), Notes: res.Notes}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Content
		if f.Path != row.MainDoc {
			d.Assets = append(d.Assets, f.Path)
		}
	}
	v := res.Verdict
	d.Verdict, d.VerdictClass, d.Next = verdictText(v.Result, v.WaiverCount), string(v.Result), next(v.Must, v.Result)
	d.Must, d.Should, d.Info, d.Score, d.Relaxed = v.Must, v.Should, v.Info, v.Score, v.RelaxedCount
	d.setRadar(v.Radar)
	d.addFindings(res.Findings, byPath)
	data, err := d.render(byPath)
	if err != nil {
		return nil, err
	}
	name := path.Base(row.Slug)
	if name == "." || name == "/" || name == "" {
		name = "bundle"
	}
	return contentReport{name: fmt.Sprintf("%s-%s-report.html", name, row.CreatedAt.UTC().Format(time.DateOnly)), data: data}, nil
}

type contentReport struct {
	name string
	data []byte
}

func (h contentReport) VisitGetContentReviewReportResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(h.data)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", h.name))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(h.data)
	return err
}
