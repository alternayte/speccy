package review

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
)

func bundleAt(slug, ref, mainDoc, profile string) pgdb.Bundle {
	return pgdb.Bundle{ID: uuid.New(), Slug: slug, SourceRef: dbtype.JSON(ref), MainDoc: mainDoc, ProfileKey: profile}
}

// A link target copied from a markdown link is percent-encoded (#51). It resolves like the
// plain path.
func TestFindTargetDecodesThePath(t *testing.T) {
	prd := bundleAt("docs/PRD - X", `{"dir":"docs","file":"PRD - X.md"}`, "PRD - X.md", "prd")
	sdd := bundleAt("docs/SDD - X", `{"dir":"docs","file":"SDD - X.md"}`, "SDD - X.md", "sdd")
	all := []pgdb.Bundle{prd, sdd}
	if got := findTarget(all, sdd, "PRD%20-%20X.md"); got == nil || got.ID != prd.ID {
		t.Errorf("findTarget = %v, want the PRD bundle", got)
	}
}

// An adopted link resolves to the bundle of its target path in the same source, and a link of
// the same kind that the doc names itself replaces it.
func TestAdoptedLinkStandsUntilTheDocNamesItsOwn(t *testing.T) {
	src := uuid.New().String()
	prd := bundleAt("docs/PRD", `{"source_id":"`+src+`","dir":"docs","file":"PRD.md"}`, "PRD.md", "prd")
	other := bundleAt("docs/OTHER", `{"source_id":"`+src+`","dir":"docs","file":"OTHER.md"}`, "OTHER.md", "prd")
	sdd := bundleAt("docs/SDD", `{"source_id":"`+src+`","dir":"docs","file":"SDD.md"}`, "SDD.md", "sdd")
	all := []pgdb.Bundle{prd, other, sdd}
	adopted := []adoptedLink{{kind: "implements", target: "docs/PRD.md"}}

	links := resolveLinksIn(all, sdd, []byte("# SDD\n"), adopted, nil, nil)
	if len(links) != 1 || links[0].origin != originAdopted || links[0].target == nil || links[0].target.ID != prd.ID {
		t.Fatalf("links = %+v, want one adopted link to the PRD", links)
	}

	own := []byte("---\ntype: sdd\nlinks:\n  - kind: implements\n    target: OTHER.md\n---\n# SDD\n")
	links = resolveLinksIn(all, sdd, own, adopted, nil, nil)
	if len(links) != 1 || links[0].origin != originFrontmatter || links[0].target.ID != other.ID {
		t.Fatalf("links = %+v, want only the doc's own link", links)
	}
}
