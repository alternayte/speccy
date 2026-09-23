package review_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// #75, #76: the fix for a missing upstream link offers the PRD, writes the link itself, and
// keeps a JSON frontmatter inside an HTML comment as it is. The rail hears at once that the
// finding is fixed.
func TestAcceptLink_WrappedJSON(t *testing.T) {
	sdd := "<!--\n---\n{\n  \"type\": \"sdd\",\n  \"title\": \"SDD - Pay\"\n}\n---\n-->\n\n# SDD - Pay\n\nThe service retries a payment.\n"
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{
				"pay/PRD - Pay.md": "---\ntype: prd\n---\n# PRD - Pay\n\nCustomers pay once.\n",
				"pay/SDD - Pay.md": sdd,
			})
			run, fs, _ := en.latest(t, "pay/SDD - Pay")
			up := findingsOf(fs, review.HasUpstreamSlug)
			if len(up) != 1 || len(findingsOf(fs, review.FrontmatterReadableSlug)) != 0 {
				t.Fatalf("findings %+v: want one has-upstream finding and a readable frontmatter", fs)
			}
			a := &review.API{DB: en.bundles.DB, Workspace: en.bundles.Workspace, Service: en.reviews, Change: en.bundles.Change}
			res, err := a.SuggestFix(ctx, api.SuggestFixRequestObject{RunId: run.ID, FindingId: up[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			s := res.(api.SuggestFix200JSONResponse)
			if s.LinkChoices == nil || len(*s.LinkChoices) != 1 || (*s.LinkChoices)[0].Path != "pay/PRD - Pay.md" {
				t.Fatalf("choices %+v", s.LinkChoices)
			}
			to := (*s.LinkChoices)[0].DocId
			out, err := a.AcceptFix(ctx, api.AcceptFixRequestObject{RunId: run.ID, FindingId: up[0].ID, Body: &api.AcceptFixRequest{LinkTo: &to}})
			if err != nil {
				t.Fatal(err)
			}
			if w := out.(api.AcceptFix200JSONResponse); w.Result != api.Fixed || !w.Changed {
				t.Fatalf("accept = %+v", w)
			}
			disk, _ := os.ReadFile(filepath.Join(en.dir, "pay", "SDD - Pay.md"))
			want := "<!--\n---\n{\n  \"type\": \"sdd\",\n  \"title\": \"SDD - Pay\",\n  \"links\": [\n    {\n      \"kind\": \"implements\",\n      \"target\": \"PRD - Pay.md\"\n    }\n  ]\n}\n---\n-->\n\n# SDD - Pay\n"
			if !strings.HasPrefix(string(disk), want) {
				t.Fatalf("file:\n%s", disk)
			}
			q := en.bundles.DB.Queries()
			b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: "pay/SDD - Pay"})
			if _, fs, _ := en.latest(t, b.Slug); len(findingsOf(fs, review.HasUpstreamSlug)) != 0 {
				t.Error("the new version still fails links.has-upstream")
			}
		})
	}
}
