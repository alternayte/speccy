package review_test

import (
	"context"
	"testing"

	"github.com/alternayte/speccy/internal/features/review"
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
