package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/source/local"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// maxZipFiles bounds the number of entries in an imported .zip file.
const maxZipFiles = 1000

// ImportBundle creates the bundles of a .md file, a .zip file, a dropped folder, or pasted
// markdown (REQ-008). A folder with two or more spec docs gives one bundle per doc, as the scan
// of a folder on disk does. In local mode the files go to a new folder under the served folder.
func (a *API) ImportBundle(ctx context.Context, req api.ImportBundleRequestObject) (api.ImportBundleResponseObject, error) {
	in, err := readImportForm(req.Body)
	if err != nil {
		return nil, err
	}
	files, defaultName, err := importFiles(in)
	if err != nil {
		return nil, err
	}
	if in.docs != nil {
		if files, err = a.chosen(files, in.docs); err != nil {
			return nil, err
		}
	} else if files, err = a.typed(files, in.profile); err != nil {
		// A doc the person hands to Speccy needs no type: Speccy writes the one the dialog
		// showed into the file it creates (REQ-008).
		return nil, err
	}
	if err := source.CheckLimits(files, a.Service.limits(ctx)); err != nil {
		return nil, err
	}
	scan, err := local.FromFS(memFS(files)).Scan(source.RepoConfig{})
	if err != nil {
		return nil, err
	}
	if len(scan.Bundles) == 0 {
		for _, p := range scan.Problems {
			return nil, kernel.Invalid("no_main_doc", "%s: %s", p.Path, p.Message)
		}
		return nil, kernel.Invalid("no_main_doc", "The import has no spec doc. Give at least one markdown file a doc type.")
	}
	name := slugify(in.name)
	if name == "" {
		name = slugify(defaultName)
	}
	if name == "" {
		name = slugify(scan.Bundles[0].Main.Title)
	}
	if name == "" {
		return nil, kernel.Invalid("no_name", "Give the bundle a name.")
	}

	s := a.Service
	if len(in.links) > 0 {
		if files, err = linked(files, scan, in.links, name, s.Local != nil); err != nil {
			return nil, err
		}
		if scan, err = local.FromFS(memFS(files)).Scan(source.RepoConfig{}); err != nil {
			return nil, err
		}
	}

	var made []pgdb.SpecDoc
	if s.Local == nil {
		// The spec docs of one folder make one bundle.
		byFolder := map[string][]NewDoc{}
		var folders []string
		for _, fb := range scan.Bundles {
			if _, ok := byFolder[fb.Folder]; !ok {
				folders = append(folders, fb.Folder)
			}
			byFolder[fb.Folder] = append(byFolder[fb.Folder], NewDoc{Slug: dbSlug(name, fb.Slug), Main: fb.Main, Files: fb.Files})
		}
		sort.Strings(folders)
		for _, f := range folders {
			docs, err := s.CreateDBBundle(ctx, dbSlug(name, f), byFolder[f], a.user(ctx))
			if err != nil {
				return nil, err
			}
			made = append(made, docs...)
		}
	} else {
		if _, err := s.createLocal(ctx, name, files); err != nil {
			return nil, err
		}
		if made, err = s.bundlesUnder(ctx, name); err != nil {
			return nil, err
		}
	}
	// The response holds each bundle the import made, with its spec docs.
	q := s.DB.Queries()
	waiting, err := a.waiting(ctx)
	if err != nil {
		return nil, err
	}
	out := api.ImportBundle201JSONResponse{Items: []api.Bundle{}, Problems: []api.BundleProblem{}}
	seen := map[uuid.UUID]bool{}
	for _, d := range made {
		if seen[d.BundleID] {
			continue
		}
		seen[d.BundleID] = true
		b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: s.Workspace, ID: d.BundleID})
		if err != nil {
			return nil, err
		}
		docs, err := a.specDocs(ctx, q, b, waiting)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, bundleToAPI(b, docs))
	}
	// What the scan of the import could not use: the dialog shows it (#66).
	for _, p := range scan.Problems {
		out.Problems = append(out.Problems, api.BundleProblem{Path: p.Path, Message: p.Message})
	}
	return out, nil
}

// PreviewImport lists the markdown files of an import: the type each names, the profile its
// headings fit, and the links Speccy offers between them. It makes nothing.
func (a *API) PreviewImport(ctx context.Context, req api.PreviewImportRequestObject) (api.PreviewImportResponseObject, error) {
	in, err := readImportForm(req.Body)
	if err != nil {
		return nil, err
	}
	files, _, err := importFiles(in)
	if err != nil {
		return nil, err
	}
	out := api.PreviewImport200JSONResponse{Docs: []api.ImportDoc{}, Links: []api.SuggestedLink{}}
	var choices []DocChoice
	for _, f := range files {
		if !source.IsMarkdown(f.Path) {
			continue
		}
		fm, _, _ := source.ReadFrontmatter(f.Content)
		d := api.ImportDoc{Path: f.Path, Title: docTitle(f.Content)}
		if d.Title == "" {
			d.Title = f.Path
		}
		key := fm.Type
		if key != "" {
			d.Type = &key
		} else if g, ok := profile.Guess(a.Profiles(), f.Content); ok && !source.NeverASpec[strings.ToLower(path.Base(f.Path))] {
			d.Guess, key = &g, g
		}
		out.Docs = append(out.Docs, d)
		choices = append(choices, DocChoice{Path: f.Path, Profile: key})
	}
	for _, l := range SuggestLinks(a.Profiles(), choices, nil) {
		out.Links = append(out.Links, api.SuggestedLink{From: l.From, To: l.To, Kind: l.Kind})
	}
	return out, nil
}

// chosen writes the profile a person gave each markdown file into its frontmatter. A file
// given no profile keeps what it names, so an untyped file stays a carried file or nothing.
func (a *API) chosen(files []source.File, docs []DocChoice) ([]source.File, error) {
	want := map[string]string{}
	for _, d := range docs {
		want[d.Path] = d.Profile
	}
	for i, f := range files {
		key, ok := want[f.Path]
		if !ok || key == "" || !source.IsMarkdown(f.Path) {
			continue
		}
		if _, ok := a.Profiles()[key]; !ok {
			return nil, kernel.Invalid("no_profile", "There is no doc type %q.", key)
		}
		next, err := source.SetKeys(f.Content, [][2]string{{"type", key}})
		if err != nil {
			return nil, kernel.Invalid("bad_frontmatter", "%s: %s.", f.Path, err.Error())
		}
		files[i].Content = next
	}
	return files, nil
}

// linked writes each confirmed link into the frontmatter of the doc that links. The target is
// the slug the target doc's bundle gets: a slug in the store, and a path relative to the doc
// on disk, as a frontmatter target reads in each mode.
func linked(files []source.File, scan *local.Scan, links []SuggestedLink, name string, onDisk bool) ([]source.File, error) {
	slugOf := map[string]string{}
	for _, b := range scan.Bundles {
		slugOf[path.Join(b.Dir, b.Main.Path)] = b.Slug
	}
	for _, l := range links {
		if !checkLinkKind(l.Kind) {
			return nil, kernel.Invalid("bad_link", "There is no link kind %q.", l.Kind)
		}
		to, ok := slugOf[l.To]
		if !ok {
			return nil, kernel.Invalid("bad_link", "%s is not a spec doc of this import, so nothing can link to it.", l.To)
		}
		target := dbSlug(name, to)
		if onDisk {
			rel, err := filepath.Rel(filepath.Dir(filepath.FromSlash(l.From)), filepath.FromSlash(l.To))
			if err != nil {
				return nil, kernel.Invalid("bad_link", "%s is not a path Speccy can link to.", l.To)
			}
			target = filepath.ToSlash(rel)
		}
		found := false
		for i, f := range files {
			if f.Path != l.From {
				continue
			}
			next, err := source.AddLink(f.Content, l.Kind, target)
			if err != nil {
				return nil, kernel.Invalid("bad_frontmatter", "%s: %s.", f.Path, err.Error())
			}
			files[i].Content, found = next, true
		}
		if !found {
			return nil, kernel.Invalid("bad_link", "%s is not a file of this import.", l.From)
		}
	}
	return files, nil
}

// dbSlug is the store slug of a bundle the scan of an import found: the import's name for the
// folder itself, and the name with the doc's path for each doc of a split folder.
func dbSlug(name, scanSlug string) string {
	if scanSlug == "." || scanSlug == "" {
		return name
	}
	return name + "-" + slugify(scanSlug)
}

// memFS is the import's files as a file system, so the scan of a folder on disk reads them.
func memFS(files []source.File) fs.FS {
	m := fstest.MapFS{}
	for _, f := range files {
		m[f.Path] = &fstest.MapFile{Data: f.Content, Mode: 0o644}
	}
	return m
}

type importForm struct {
	name     string
	profile  string
	fileName string
	file     []byte
	text     string
	hasText  bool
	// folder holds the files of a dropped folder, by path.
	folder []source.File
	// docs is nil when the form names no choice per file.
	docs  []DocChoice
	links []SuggestedLink
}

func readImportForm(r *multipart.Reader) (importForm, error) {
	var in importForm
	var total int
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return in, kernel.Invalid("bad_form", "The form data does not parse: %s.", err.Error())
		}
		data, err := io.ReadAll(io.LimitReader(part, source.CeilingBundleBytes+1))
		if err != nil {
			return in, err
		}
		total += len(data)
		if len(data) > source.CeilingBundleBytes || total > source.CeilingBundleBytes {
			return in, kernel.TooLarge("bundle_too_large", "The upload is larger than %d MB, which is the most Speccy accepts for one bundle.", source.CeilingBundleBytes>>20)
		}
		switch part.FormName() {
		case "name":
			in.name = strings.TrimSpace(string(data))
		case "profile":
			in.profile = strings.TrimSpace(string(data))
		case "file":
			in.fileName, in.file = part.FileName(), data
		case "files":
			p, err := source.CleanPath(strings.ReplaceAll(partPath(part), `\`, "/"))
			if err != nil {
				return in, kernel.Invalid("bad_path", "%s.", err.Error())
			}
			if len(in.folder) == maxZipFiles {
				return in, kernel.TooLarge("bundle_too_large", "The folder has more than %d files.", maxZipFiles)
			}
			in.folder = append(in.folder, source.File{Path: p, Content: data})
		case "text":
			in.text, in.hasText = string(data), true
		case "docs":
			var docs []struct{ Path, Profile string }
			if err := json.Unmarshal(data, &docs); err != nil {
				return in, kernel.Invalid("bad_form", "docs is not a JSON list of {path, profile}.")
			}
			in.docs = []DocChoice{}
			for _, d := range docs {
				in.docs = append(in.docs, DocChoice{Path: d.Path, Profile: strings.TrimSpace(d.Profile)})
			}
		case "links":
			var links []struct{ From, To, Kind string }
			if err := json.Unmarshal(data, &links); err != nil {
				return in, kernel.Invalid("bad_form", "links is not a JSON list of {from, to, kind}.")
			}
			for _, l := range links {
				in.links = append(in.links, SuggestedLink{From: l.From, To: l.To, Kind: l.Kind})
			}
		}
	}
	sent := 0
	for _, b := range []bool{in.file != nil, in.hasText, len(in.folder) > 0} {
		if b {
			sent++
		}
	}
	if sent != 1 {
		return in, kernel.Invalid("bad_import", "Send exactly one of a file, a folder, or pasted text.")
	}
	return in, nil
}

// importFiles returns the bundle files and a default bundle name.
func importFiles(in importForm) ([]source.File, string, error) {
	if len(in.folder) > 0 {
		files, top := dropTop(in.folder)
		source.Sort(files)
		return files, top, nil
	}
	if in.hasText {
		return []source.File{{Path: "SPEC.md", Content: []byte(in.text)}}, "", nil
	}
	base := path.Base(strings.ReplaceAll(in.fileName, `\`, "/"))
	ext := strings.ToLower(path.Ext(base))
	stem := strings.TrimSuffix(base, path.Ext(base))
	switch ext {
	case ".md", ".markdown":
		p, err := source.CleanPath(base)
		if err != nil {
			return nil, "", kernel.Invalid("bad_path", "%s.", err.Error())
		}
		return []source.File{{Path: p, Content: in.file}}, stem, nil
	case ".zip":
		files, top, err := unzip(in.file)
		if err != nil {
			return nil, "", err
		}
		if top != "" {
			stem = top
		}
		return files, stem, nil
	}
	return nil, "", kernel.Invalid("bad_import", "Speccy imports .md and .zip files. %s is neither.", base)
}

// dropTop removes the one top folder that every path of a dropped folder shares, and returns
// its name as the default bundle name.
func dropTop(files []source.File) ([]source.File, string) {
	i := strings.IndexByte(files[0].Path, '/')
	if i <= 0 {
		return files, ""
	}
	top := files[0].Path[:i]
	for _, f := range files {
		if !strings.HasPrefix(f.Path, top+"/") {
			return files, ""
		}
	}
	for j := range files {
		files[j].Path = strings.TrimPrefix(files[j].Path, top+"/")
	}
	return files, top
}

// unzip reads the files of a .zip archive. When every entry is under one top folder, the
// folder is removed from the paths and returned as top.
func unzip(data []byte) ([]source.File, string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", kernel.Invalid("bad_zip", "The .zip file does not open: %s.", err.Error())
	}
	var files []source.File
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(path.Base(name), ".") {
			continue
		}
		if !f.Mode().IsRegular() {
			continue // no symlinks
		}
		p, err := source.CleanPath(name)
		if err != nil {
			return nil, "", kernel.Invalid("bad_zip", "The .zip file has an entry that Speccy cannot use: %s.", err.Error())
		}
		if len(files) == maxZipFiles {
			return nil, "", kernel.TooLarge("bundle_too_large", "The .zip file has more than %d files.", maxZipFiles)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", kernel.Invalid("bad_zip", "%s does not open: %s.", p, err.Error())
		}
		// Read one byte more than the limit, so a false size in the header cannot hide a large file.
		content, err := io.ReadAll(io.LimitReader(rc, source.CeilingFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, "", kernel.Invalid("bad_zip", "%s does not read: %s.", p, err.Error())
		}
		if len(content) > source.CeilingFileBytes {
			return nil, "", kernel.TooLarge("file_too_large", "%s is larger than %d MB, which is the most Speccy accepts for one file.", p, source.CeilingFileBytes>>20)
		}
		total += int64(len(content))
		if total > source.CeilingBundleBytes {
			return nil, "", kernel.TooLarge("bundle_too_large", "The files in the .zip file are larger than %d MB together, which is the most Speccy accepts for one bundle.", source.CeilingBundleBytes>>20)
		}
		files = append(files, source.File{Path: p, Content: content})
	}
	if len(files) == 0 {
		return nil, "", kernel.Invalid("bad_zip", "The .zip file has no files.")
	}
	top := ""
	if i := strings.IndexByte(files[0].Path, '/'); i > 0 {
		top = files[0].Path[:i]
		for _, f := range files {
			if !strings.HasPrefix(f.Path, top+"/") {
				top = ""
				break
			}
		}
	}
	if top != "" {
		for i := range files {
			files[i].Path = strings.TrimPrefix(files[i].Path, top+"/")
		}
	}
	source.Sort(files)
	return files, top, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify returns a folder name: lowercase letters, digits, and single hyphens.
func slugify(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// typed gives the import a main doc when no file names a type: it writes the type line into
// the one markdown file the import has. The caller's profile wins; otherwise Speccy guesses.
func (a *API) typed(files []source.File, want string) ([]source.File, error) {
	if _, err := source.FindMainDoc(files); err == nil {
		return files, nil
	}
	var only int
	count := 0
	for i, f := range files {
		if !strings.Contains(f.Path, "/") && source.IsMarkdown(f.Path) {
			only, count = i, count+1
		}
	}
	if count != 1 {
		return files, nil // more than one candidate, or none: the caller reports it
	}
	key := want
	if key == "" {
		k, ok := profile.Guess(a.Profiles(), files[only].Content)
		if !ok {
			return nil, kernel.Invalid("no_profile", "%s names no type, and no doc type fits its headings. Pick a type and import it again.", files[only].Path)
		}
		key = k
	}
	if _, ok := a.Profiles()[key]; !ok {
		return nil, kernel.Invalid("no_profile", "There is no doc type %q.", key)
	}
	files[only].Content = source.AddTypeLine(files[only].Content, key)
	return files, nil
}

// partPath is the file name a form part was sent with, folders included. FileName drops the
// folders, and a dropped folder needs them.
func partPath(part *multipart.Part) string {
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return part.FileName()
	}
	return params["filename"]
}
