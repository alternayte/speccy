package version

import (
	"context"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/store"
)

// Current is the text of a bundle's current version, to move anchors from older versions to it
// (SDD §8.8).
type Current struct {
	ID    uuid.UUID
	main  string
	files map[string][]byte
	docs  map[string]section.Doc
}

// LoadCurrent reads the current version of b.
func LoadCurrent(ctx context.Context, q store.Querier, b pgdb.SpecDoc) (*Current, error) {
	c := &Current{ID: b.CurrentVersionID.UUID, main: b.DocPath, files: map[string][]byte{}, docs: map[string]section.Doc{}}
	if !b.CurrentVersionID.Valid {
		return c, nil
	}
	files, err := Files(ctx, q, c.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		c.files[f.Path] = f.Content
	}
	return c, nil
}

// Anchor returns an in the current version. ok is false when the anchor is detached: its file
// is gone, or its text cannot be found. Only the main doc has sections.
func (c *Current) Anchor(an anchor.Anchor) (anchor.Anchor, bool) {
	src, ok := c.files[an.File]
	if !ok {
		return an, false
	}
	doc, ok := c.docs[an.File]
	if !ok {
		if an.File == c.main {
			doc = section.Parse(src)
		}
		c.docs[an.File] = doc
	}
	return anchor.Reanchor(an, src, doc)
}

// File returns the content of a file in the current version, or nil.
func (c *Current) File(path string) []byte { return c.files[path] }
