package profile

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// Registry holds the current profiles of a local folder: built-ins with .speccy/profiles over
// them. A profile file that does not load is listed as a problem, and the last good set stays.
type Registry struct {
	DB        *store.DB
	Workspace uuid.UUID
	Dir       string // .speccy/profiles

	mu       sync.RWMutex
	current  map[string]Versioned
	problems []string
}

// Reload loads the profiles again and records new versions.
func (r *Registry) Reload(ctx context.Context) error {
	loaded, loadErr := LoadLocal(r.Dir)
	var problems []string
	if loadErr != nil {
		for _, e := range unwrapAll(loadErr) {
			problems = append(problems, e.Error())
		}
	}
	versions, err := Record(ctx, r.DB, r.Workspace, loaded, "local")
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.current = versions
	r.problems = problems
	r.mu.Unlock()
	return nil
}

func unwrapAll(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// Current returns the profiles by key.
func (r *Registry) Current() map[string]Versioned {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Problems returns the profile files that did not load.
func (r *Registry) Problems() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.problems...)
}

// API serves the profile endpoints.
type API struct {
	Registry *Registry
}

// ListProfiles lists the current profiles.
func (a *API) ListProfiles(context.Context, api.ListProfilesRequestObject) (api.ListProfilesResponseObject, error) {
	if a.Registry == nil {
		return nil, errors.New("no profile registry")
	}
	out := api.ProfileList{Items: []api.Profile{}, Problems: a.Registry.Problems()}
	if out.Problems == nil {
		out.Problems = []string{}
	}
	for _, v := range a.Registry.Current() {
		out.Items = append(out.Items, api.Profile{Key: v.Profile.Key, Name: v.Profile.Name, Version: v.Version, Origin: v.Origin})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Key < out.Items[j].Key })
	return api.ListProfiles200JSONResponse(out), nil
}
