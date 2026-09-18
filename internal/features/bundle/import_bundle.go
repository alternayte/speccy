package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path"
	"regexp"
	"strings"

	pgdb "github.com/alternayte/speccy/db/postgres"
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
	main, err := source.FindMainDoc(files)
	if err != nil {
		return nil, kernel.Invalid("no_main_doc", "The import has %s. A bundle needs exactly one markdown file with a type field in its frontmatter.", err.Error())
	}
	if err := source.CheckLimits(files); err != nil {
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
		b, err := s.CreateDB(ctx, name, files, a.user())
		if err != nil {
			return nil, err
		}
		out, err := toAPI(ctx, s.DB.Queries(), b)
		if err != nil {
			return nil, err
		}
		return api.ImportBundle201JSONResponse(out), nil
	}

	s.mu.Lock()
	dir, err := s.Local.CreateBundle(name, files)
	if err == nil {
		err = s.syncLocked(ctx)
	}
	s.mu.Unlock()
	if err != nil {
		if _, ok := kernel.AsError(err); ok {
			return nil, err
		}
		return nil, kernel.Invalid("import_failed", "%s.", err.Error())
	}
	q := s.DB.Queries()
	b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: dir})
	if err != nil {
		return nil, fmt.Errorf("find imported bundle %s: %w", dir, err)
	}
	out, err := toAPI(ctx, q, b)
	if err != nil {
		return nil, err
	}
	return api.ImportBundle201JSONResponse(out), nil
}

type importForm struct {
	name     string
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
		data, err := io.ReadAll(io.LimitReader(part, source.MaxBundleBytes+1))
		if err != nil {
			return in, err
		}
		if len(data) > source.MaxBundleBytes {
			return in, kernel.TooLarge("bundle_too_large", "The upload is larger than 50 MB, which is the limit for one bundle.")
		}
		switch part.FormName() {
		case "name":
			in.name = strings.TrimSpace(string(data))
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
		content, err := io.ReadAll(io.LimitReader(rc, source.MaxFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, "", kernel.Invalid("bad_zip", "%s does not read: %s.", p, err.Error())
		}
		if len(content) > source.MaxFileBytes {
			return nil, "", kernel.TooLarge("file_too_large", "%s is larger than 10 MB, which is the limit for one file.", p)
		}
		total += int64(len(content))
		if total > source.MaxBundleBytes {
			return nil, "", kernel.TooLarge("bundle_too_large", "The files in the .zip file are larger than 50 MB together, which is the limit for one bundle.")
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
