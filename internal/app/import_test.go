package app_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store/storetest"
)

func form(t *testing.T, fields map[string]string, files map[string]string) *multipart.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for p, content := range files {
		fw, err := w.CreateFormFile("files", p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(content))
	}
	_ = w.Close()
	return multipart.NewReader(&buf, w.Boundary())
}

// A dropped folder with a PRD and an SDD that name no type gives one bundle per doc, with the
// profile the person picked for each, and the SDD links the PRD when the person confirms it.
func TestImportSplitsAFolderAndLinksTheDocs(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			folder := map[string]string{
				"feature-x/PRD - X.md":  "# X\n\n## Problem\n\nSlow.\n",
				"feature-x/SDD - X.md":  "# X design\n\n## Context\n\nFast.\n",
				"feature-x/notes.md":    "# Notes\n",
				"feature-x/diagram.png": "png",
			}
			preview, err := env.app.API.PreviewImport(as("member"), api.PreviewImportRequestObject{Body: form(t, nil, folder)})
			if err != nil {
				t.Fatal(err)
			}
			if n := len(preview.(api.PreviewImport200JSONResponse).Docs); n != 3 {
				t.Errorf("preview lists %d docs, want the 3 markdown files", n)
			}

			res, err := env.app.API.ImportBundle(as("member"), api.ImportBundleRequestObject{Body: form(t, map[string]string{
				"docs":  `[{"path":"PRD - X.md","profile":"prd"},{"path":"SDD - X.md","profile":"sdd"},{"path":"notes.md","profile":""}]`,
				"links": `[{"from":"SDD - X.md","to":"PRD - X.md","kind":"implements"}]`,
			}, folder)})
			if err != nil {
				t.Fatal(err)
			}
			items := res.(api.ImportBundle201JSONResponse).Items
			slugs := map[string]string{}
			for _, b := range items {
				slugs[b.ProfileKey] = b.Slug
			}
			if len(items) != 2 || slugs["prd"] != "feature-x-prd-x" || slugs["sdd"] != "feature-x-sdd-x" {
				t.Fatalf("bundles = %+v, want one PRD and one SDD bundle", slugs)
			}
			q := env.app.Bundles.DB.Queries()
			sdd, err := q.GetSpecDocBySlug(context.Background(), pgdb.GetSpecDocBySlugParams{WorkspaceID: env.app.Workspace, Slug: slugs["sdd"]})
			if err != nil {
				t.Fatal(err)
			}
			links, err := q.ListLinksFrom(context.Background(), sdd.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 1 || links[0].Kind != "implements" || !links[0].TargetSpecDocID.Valid {
				t.Errorf("SDD links = %+v, want one resolved implements link", links)
			}
		})
	}
}
