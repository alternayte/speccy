package review

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
)

func bundleAt(slug, ref, mainDoc, profile string) pgdb.SpecDoc {
	return pgdb.SpecDoc{ID: uuid.New(), Slug: slug, SourceRef: dbtype.JSON(ref), DocPath: mainDoc, ProfileKey: profile}
}

// A link target copied from a markdown link is percent-encoded (#51). It resolves like the
// plain path.
func TestFindTargetDecodesThePath(t *testing.T) {
	prd := bundleAt("docs/PRD - X", `{"dir":"docs","file":"PRD - X.md"}`, "PRD - X.md", "prd")
	sdd := bundleAt("docs/SDD - X", `{"dir":"docs","file":"SDD - X.md"}`, "SDD - X.md", "sdd")
	all := []pgdb.SpecDoc{prd, sdd}
	if got := findTarget(all, nil, sdd, "PRD%20-%20X.md", func(string) bool { return true }); got == nil || got.ID != prd.ID {
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
	all := []pgdb.SpecDoc{prd, other, sdd}
	adopted := []adoptedLink{{kind: "implements", target: "docs/PRD.md"}}

	links := resolveLinksIn(all, nil, sdd, []byte("# SDD\n"), adopted, nil, nil, nil, nil)
	if len(links) != 1 || links[0].origin != originAdopted || links[0].target == nil || links[0].target.ID != prd.ID {
		t.Fatalf("links = %+v, want one adopted link to the PRD", links)
	}

	own := []byte("---\ntype: sdd\nlinks:\n  - kind: implements\n    target: OTHER.md\n---\n# SDD\n")
	links = resolveLinksIn(all, nil, sdd, own, adopted, nil, nil, nil, nil)
	if len(links) != 1 || links[0].origin != originFrontmatter || links[0].target.ID != other.ID {
		t.Fatalf("links = %+v, want only the doc's own link", links)
	}
}

// A target that names a bundle resolves only when exactly one spec doc in it has a profile the
// link accepts. With two, nothing resolves: Speccy never picks a doc silently.
func TestBundleSlugResolvesToTheOneAcceptedDoc(t *testing.T) {
	folder := uuid.New()
	prd := bundleAt("pay/PRD", `{"dir":"pay","file":"PRD.md"}`, "PRD.md", "prd")
	sdd := bundleAt("pay/SDD", `{"dir":"pay","file":"SDD.md"}`, "SDD.md", "sdd")
	prd.BundleID, sdd.BundleID = folder, folder
	other := bundleAt("api", `{"dir":"api","file":"SDD.md"}`, "SDD.md", "sdd")
	place := docPlace{folder: "pay", other.BundleID: "api"}
	all := []pgdb.SpecDoc{prd, sdd, other}
	onlyPRD := func(p string) bool { return p == "prd" }
	if got := findTarget(all, place, other, "pay", onlyPRD); got == nil || got.ID != prd.ID {
		t.Errorf("findTarget(pay) = %v, want the PRD", got)
	}
	if got := findTarget(all, place, other, "pay", func(string) bool { return true }); got != nil {
		t.Errorf("findTarget(pay) with two accepted docs = %v, want none", got.Slug)
	}
	if got := findTarget(all, place, sdd, "./PRD.md", func(string) bool { return false }); got == nil || got.ID != prd.ID {
		t.Errorf("findTarget(./PRD.md) = %v, want the PRD in the same bundle", got)
	}
}

// #97: a link whose target is a doc on a GitHub branch resolves to the spec doc of the source
// that holds that file, by its URL or by the github: form. A branch name can hold a slash. A
// link at a commit, or to a file that no source holds, stays an external link.
func TestGitHubDocLinkResolvesToTheSourceDoc(t *testing.T) {
	src := uuid.New()
	prd := bundleAt("docs/prd/PRD - Pay", `{"source_id":"`+src.String()+`","dir":"docs/prd","file":"PRD - Pay.md"}`, "PRD - Pay.md", "prd")
	sdd := bundleAt("pay/SDD", `{"dir":"pay"}`, "SDD.md", "sdd")
	all := []pgdb.SpecDoc{prd, sdd}
	held := githubDocs{src: {ID: src, Repo: "acme/specs", Branch: "feature/pay", ApiUrl: "https://api.github.com"}}
	for _, c := range []struct {
		target   string
		resolves bool
	}{
		{"https://github.com/acme/specs/blob/feature/pay/docs/prd/PRD%20-%20Pay.md", true},
		{"github:acme/specs@feature/pay#docs/prd/PRD - Pay.md", true},
		{"https://github.com/acme/specs/blob/main/docs/prd/PRD%20-%20Pay.md", false},
		{"https://github.com/acme/specs/blob/0123456789abcdef0123456789abcdef01234567/docs/prd/PRD%20-%20Pay.md", false},
		{"https://github.com/other/specs/blob/feature/pay/docs/prd/PRD%20-%20Pay.md", false},
	} {
		main := []byte("---\ntype: sdd\nlinks:\n  - kind: implements\n    target: \"" + c.target + "\"\n---\n# SDD\n")
		links := resolveLinksIn(all, nil, sdd, main, nil, nil, nil, nil, held)
		if len(links) != 1 {
			t.Fatalf("%s: %d links", c.target, len(links))
		}
		if got := links[0].target != nil && links[0].target.ID == prd.ID; got != c.resolves {
			t.Errorf("%s: resolves to the PRD = %v, want %v (kind %s)", c.target, got, c.resolves, links[0].targetKind)
		}
		if !c.resolves && (links[0].targetKind != "external" || links[0].external == nil) {
			t.Errorf("%s: an unresolved target must stay an external link, got %+v", c.target, links[0])
		}
	}
}
