package version

import (
	"context"
	"math"
	"strconv"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the version endpoints.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
}

// ListVersions returns one page of versions, newest first. The cursor is a version number.
func (a *API) ListVersions(ctx context.Context, req api.ListVersionsRequestObject) (api.ListVersionsResponseObject, error) {
	q := a.DB.Queries()
	b, err := Bundle(ctx, q, a.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	limit := int64(50)
	if req.Params.Limit != nil {
		limit = int64(*req.Params.Limit)
	}
	before := int64(math.MaxInt64)
	if req.Params.Cursor != nil {
		n, err := strconv.ParseInt(*req.Params.Cursor, 10, 64)
		if err != nil || n < 1 {
			return nil, kernel.Invalid("bad_cursor", "The cursor %q is not valid. Use the next_cursor of the previous page.", *req.Params.Cursor)
		}
		before = n
	}
	rows, err := q.ListVersions(ctx, pgdb.ListVersionsParams{BundleID: b.ID, BeforeNumber: before, PageSize: limit + 1})
	if err != nil {
		return nil, err
	}
	out := api.VersionList{Items: []api.Version{}}
	for i, v := range rows {
		if int64(i) == limit {
			next := strconv.FormatInt(rows[i-1].Number, 10)
			out.NextCursor = &next
			break
		}
		out.Items = append(out.Items, ToAPI(v))
	}
	return api.ListVersions200JSONResponse(out), nil
}
