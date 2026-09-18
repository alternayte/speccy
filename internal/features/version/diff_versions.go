package version

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// DiffVersions compares two versions by file and by section of the main doc (REQ-006).
func (a *API) DiffVersions(ctx context.Context, req api.DiffVersionsRequestObject) (api.DiffVersionsResponseObject, error) {
	q := a.DB.Queries()
	b, err := Bundle(ctx, q, a.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	from, err := Get(ctx, q, b, &req.Params.From)
	if err != nil {
		return nil, err
	}
	to, err := Get(ctx, q, b, &req.Params.To)
	if err != nil {
		return nil, err
	}
	fromFiles, err := Files(ctx, q, from.ID)
	if err != nil {
		return nil, err
	}
	toFiles, err := Files(ctx, q, to.ID)
	if err != nil {
		return nil, err
	}
	return api.DiffVersions200JSONResponse{
		From:     ToAPI(from),
		To:       ToAPI(to),
		Files:    DiffFiles(fromFiles, toFiles),
		Sections: DiffSections(mainDoc(fromFiles), mainDoc(toFiles)),
	}, nil
}

// DiffFiles compares two file sets. Modified text files get line operations.
func DiffFiles(from, to []source.File) []api.FileDiff {
	old := map[string][]byte{}
	for _, f := range from {
		old[f.Path] = f.Content
	}
	seen := map[string]bool{}
	var out []api.FileDiff
	for _, f := range to {
		seen[f.Path] = true
		prev, ok := old[f.Path]
		text := IsText(f.Content) && (!ok || IsText(prev))
		d := api.FileDiff{Path: f.Path, Binary: !text, Lines: []api.LineOp{}}
		switch {
		case !ok:
			d.Status = api.Added
			if text {
				d.Lines = LineDiff("", string(f.Content))
			}
		case Hash(prev) == Hash(f.Content):
			d.Status = api.Unchanged
		default:
			d.Status = api.Modified
			if text {
				d.Lines = LineDiff(string(prev), string(f.Content))
			}
		}
		out = append(out, d)
	}
	for _, f := range from {
		if seen[f.Path] {
			continue
		}
		d := api.FileDiff{Path: f.Path, Status: api.Removed, Binary: !IsText(f.Content), Lines: []api.LineOp{}}
		if !d.Binary {
			d.Lines = LineDiff(string(f.Content), "")
		}
		out = append(out, d)
	}
	sortFileDiffs(out)
	return out
}

func sortFileDiffs(d []api.FileDiff) {
	sort.Slice(d, func(i, j int) bool { return d[i].Path < d[j].Path })
}

// DiffSections matches the sections of two main docs by heading path and compares their own
// content. Sections with the same path pair up in order.
func DiffSections(from, to []byte) []api.SectionDiff {
	type sec struct {
		path []string
		hash string
		own  string
	}
	collect := func(src []byte) ([]sec, map[string][]int) {
		if src == nil {
			return nil, map[string][]int{}
		}
		var out []sec
		idx := map[string][]int{}
		for _, s := range section.Parse(src).Sections {
			k := strings.Join(s.Path, "\x00")
			idx[k] = append(idx[k], len(out))
			out = append(out, sec{path: s.Path, hash: s.Hash, own: section.Normalize(s.Own(src))})
		}
		return out, idx
	}
	olds, oldIdx := collect(from)
	news, _ := collect(to)
	used := map[int]bool{}
	var out []api.SectionDiff
	for _, n := range news {
		k := strings.Join(n.path, "\x00")
		d := api.SectionDiff{HeadingPath: n.path, Lines: []api.LineOp{}}
		match := -1
		for _, i := range oldIdx[k] {
			if !used[i] {
				match = i
				break
			}
		}
		switch {
		case match < 0:
			d.Status = api.Added
			d.Lines = LineDiff("", n.own)
		case olds[match].hash == n.hash:
			used[match] = true
			d.Status = api.Unchanged
		default:
			used[match] = true
			d.Status = api.Modified
			d.Lines = LineDiff(olds[match].own, n.own)
		}
		out = append(out, d)
	}
	for i, o := range olds {
		if !used[i] {
			out = append(out, api.SectionDiff{HeadingPath: o.path, Status: api.Removed, Lines: LineDiff(o.own, "")})
		}
	}
	return out
}

// LineDiff returns whole-line operations that turn a into b.
func LineDiff(a, b string) []api.LineOp {
	dmp := diffmatchpatch.New()
	ca, cb, lines := dmp.DiffLinesToChars(a, b)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(ca, cb, false), lines)
	out := make([]api.LineOp, 0, len(diffs))
	for _, d := range diffs {
		var op api.LineOpOp
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			op = api.Equal
		case diffmatchpatch.DiffInsert:
			op = api.Insert
		case diffmatchpatch.DiffDelete:
			op = api.Delete
		}
		out = append(out, api.LineOp{Op: op, Text: d.Text})
	}
	return out
}

func mainDoc(files []source.File) []byte {
	m, err := source.FindMainDoc(files)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if f.Path == m.Path {
			return f.Content
		}
	}
	return nil
}

// IsText reports whether content is text that a line diff can show: valid UTF-8, no NUL byte.
func IsText(content []byte) bool {
	return !bytes.ContainsRune(content, 0) && utf8.Valid(content)
}
