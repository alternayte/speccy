package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"path"
	"regexp"
	"strings"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// maxZipFiles bounds the number of entries in an imported .zip file.
const maxZipFiles = 1000

// ImportBundle creates a bundle from a .md file, a .zip file, or pasted markdown (REQ-008).
// In local mode the bundle is a new folder under the served folder.
func (a *API) ImportBundle(ctx context.Context, req api.ImportBundleRequestObject) (api.ImportBundleResponseObject, error) {
	in, err := readImportForm(req.Body)
	if err != nil {
		return nil, err
	}
	files, defaultName, err := importFiles(in)
	if err != nil {
		return nil, err
	}
	// A doc the person hands to Speccy needs no type: Speccy writes the one the dialog showed
	// into the file it creates (REQ-008).
	files, err = a.typed(files, in.profile)
	if err != nil {
		return nil, err
	}
	main, err := source.FindMainDoc(files)
	if err != nil {
		return nil, kernel.Invalid("no_main_doc", "The import has %s. A bundle needs exactly one markdown file with a type field in its frontmatter.", err.Error())
	}
	if err := source.CheckLimits(files, a.Service.limits(ctx)); err != nil {
		return nil, err
	}
	name := slugify(in.name)
	if name == "" {
		name = slugify(defaultName)
	}
	if name == "" {
		name = slugify(main.Title)
	}
	if name == "" {
		return nil, kernel.Invalid("no_name", "Give the bundle a name.")
	}

	s := a.Service
	if s.Local == nil {
		b, err := s.CreateDB(ctx, name, files, a.user(ctx))
		if err != nil {
			return nil, err
		}
		out, err := toAPI(ctx, s.DB.Queries(), b)
		if err != nil {
			return nil, err
		}
		return api.ImportBundle201JSONResponse(out), nil
	}

	b, err := s.createLocal(ctx, name, files)
	if err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, s.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	return api.ImportBundle201JSONResponse(out), nil
}

type importForm struct {
	name     string
	profile  string
	fileName string
	file     []byte
	text     string
	hasText  bool
}

func readImportForm(r *multipart.Reader) (importForm, error) {
	var in importForm
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
		if len(data) > source.CeilingBundleBytes {
			return in, kernel.TooLarge("bundle_too_large", "The upload is larger than %d MB, which is the most Speccy accepts for one bundle.", source.CeilingBundleBytes>>20)
		}
		switch part.FormName() {
		case "name":
			in.name = strings.TrimSpace(string(data))
		case "profile":
			in.profile = strings.TrimSpace(string(data))
		case "file":
			in.fileName, in.file = part.FileName(), data
		case "text":
			in.text, in.hasText = string(data), true
		}
	}
	if (in.file != nil) == in.hasText {
		return in, kernel.Invalid("bad_import", "Send exactly one of a file or pasted text.")
	}
	return in, nil
}

// importFiles returns the bundle files and a default bundle name.
func importFiles(in importForm) ([]source.File, string, error) {
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
