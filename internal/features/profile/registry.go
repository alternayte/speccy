package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Registry holds the current profiles. In local mode they are the built-ins with
// .speccy/profiles over them. In hosted mode the store holds them, edited in the app (REQ-013);
// the built-ins seed a doc type that has no profile yet. A profile that does not load is listed
// as a problem, and the last good set stays.
type Registry struct {
	DB        *store.DB
	Workspace uuid.UUID
	Dir       string // .speccy/profiles in local mode; "" in hosted mode
	Hosted    bool

	mu       sync.RWMutex
	current  map[string]Versioned
	problems []string
}

// Reload loads the profiles again and records new versions.
func (r *Registry) Reload(ctx context.Context) error {
	var versions map[string]Versioned
	var problems []string
	var err error
	if r.Hosted {
		versions, problems, err = r.loadHosted(ctx)
	} else {
		loaded, loadErr := LoadLocal(r.Dir)
		if loadErr != nil {
			for _, e := range unwrapAll(loadErr) {
				problems = append(problems, e.Error())
			}
		}
		versions, err = Record(ctx, r.DB, r.Workspace, loaded, "local")
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.current = versions
	r.problems = problems
	r.mu.Unlock()
	return nil
}

// loadHosted seeds missing built-ins, then reads the current version of every stored profile.
func (r *Registry) loadHosted(ctx context.Context) (map[string]Versioned, []string, error) {
	q := r.DB.Queries()
	builtins, err := Builtins()
	if err != nil {
		return nil, nil, err
	}
	seed := map[string]Loaded{}
	for _, l := range builtins {
		if _, err := q.GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: r.Workspace, Key: l.Profile.Key}); errors.Is(err, sql.ErrNoRows) {
			seed[l.Profile.Key] = l
		} else if err != nil {
			return nil, nil, err
		}
	}
	if len(seed) > 0 {
		if _, err := Record(ctx, r.DB, r.Workspace, seed, "system"); err != nil {
			return nil, nil, err
		}
	}
	rows, err := q.ListProfiles(ctx, r.Workspace)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]Versioned{}
	var problems []string
	for _, p := range rows {
		v, err := q.GetProfileVersion(ctx, pgdb.GetProfileVersionParams{ProfileID: p.ID, Version: p.CurrentVersion})
		if err != nil {
			return nil, nil, err
		}
		l, err := Parse(p.Key, []byte(v.Yaml), func(string) ([]byte, error) { return []byte(v.Template), nil })
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		l.Origin = v.Origin
		out[p.Key] = Versioned{Loaded: l, Version: p.CurrentVersion}
	}
	return out, problems, nil
}

// Save validates a profile and stores it as a new version (REQ-012, REQ-013). In local mode it
// writes .speccy/profiles/<key>.yaml and its template file.
func (r *Registry) Save(ctx context.Context, key string, src, template []byte, by string) (Versioned, error) {
	l, err := Parse(key, src, func(string) ([]byte, error) { return template, nil })
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return Versioned{}, kernel.Invalid("invalid_profile", "The profile is not valid:\n%s", strings.Join(ve.Errors, "\n"))
		}
		return Versioned{}, err
	}
	if l.Profile.Key != key {
		return Versioned{}, kernel.Invalid("key_changed", "The profile key is %q. It must stay %q.", l.Profile.Key, key)
	}
	if r.Hosted {
		l.Origin = "edited by " + by
		if _, err := Record(ctx, r.DB, r.Workspace, map[string]Loaded{key: l}, by); err != nil {
			return Versioned{}, err
		}
	} else {
		if r.Dir == "" {
			return Versioned{}, kernel.Invalid("no_profiles_dir", "This server has no profiles folder.")
		}
		tp := filepath.Clean(filepath.FromSlash(l.Profile.Template))
		if filepath.IsAbs(tp) || strings.HasPrefix(tp, "..") {
			return Versioned{}, kernel.Invalid("bad_template_path", "The template path must stay inside .speccy/profiles.")
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(r.Dir, tp)), 0o755); err != nil {
			return Versioned{}, err
		}
		if err := os.WriteFile(filepath.Join(r.Dir, tp), template, 0o644); err != nil {
			return Versioned{}, err
		}
		if err := os.WriteFile(filepath.Join(r.Dir, key+".yaml"), src, 0o644); err != nil {
			return Versioned{}, err
		}
	}
	if err := r.Reload(ctx); err != nil {
		return Versioned{}, err
	}
	v, ok := r.Current()[key]
	if !ok {
		return Versioned{}, fmt.Errorf("profile %s did not load after save", key)
	}
	return v, nil
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
	People   kernel.Directory
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

func (a *API) profileRow(ctx context.Context, key string) (pgdb.Profile, error) {
	p, err := a.Registry.DB.Queries().GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: a.Registry.Workspace, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return p, kernel.NotFound("profile_not_found", "No profile has the key %s.", key)
	}
	return p, err
}

// CanEdit reports whether the actor can edit a profile: an admin, or a maintainer of it (SDD §3).
func CanEdit(ctx context.Context, q store.Querier, workspace uuid.UUID, key string) (bool, error) {
	a := kernel.ActorFrom(ctx)
	if a.IsAdmin() {
		return true, nil
	}
	if a.Guest != nil || a.UserID == "" {
		return false, nil
	}
	return q.IsProfileMaintainer(ctx, pgdb.IsProfileMaintainerParams{WorkspaceID: workspace, Key: key, UserID: a.UserID})
}

func (a *API) detail(ctx context.Context, key string) (api.ProfileDetail, error) {
	v, ok := a.Registry.Current()[key]
	if !ok {
		return api.ProfileDetail{}, kernel.NotFound("profile_not_found", "No profile has the key %s, or it does not load.", key)
	}
	row, err := a.profileRow(ctx, key)
	if err != nil {
		return api.ProfileDetail{}, err
	}
	q := a.Registry.DB.Queries()
	versions, err := q.ListProfileVersions(ctx, row.ID)
	if err != nil {
		return api.ProfileDetail{}, err
	}
	maint, err := q.ListProfileMaintainers(ctx, row.ID)
	if err != nil {
		return api.ProfileDetail{}, err
	}
	can, err := CanEdit(ctx, q, a.Registry.Workspace, key)
	if err != nil {
		return api.ProfileDetail{}, err
	}
	out := api.ProfileDetail{Key: key, Name: v.Profile.Name, Version: v.Version, Origin: v.Origin, Yaml: string(v.Source),
		Template: string(v.TemplateText), Maintainers: []string{}, CanEdit: can, Editable: a.Registry.Hosted || a.Registry.Dir != ""}
	out.Maintainers = append(out.Maintainers, maint...)
	for _, pv := range versions {
		out.Versions = append(out.Versions, struct {
			CreatedAt time.Time `json:"created_at"`
			CreatedBy string    `json:"created_by"`
			Version   int64     `json:"version"`
		}{CreatedAt: pv.CreatedAt.UTC(), CreatedBy: kernel.PersonByID(ctx, a.People, pv.CreatedBy).Label(), Version: pv.Version})
	}
	if out.Versions == nil {
		out.Versions = []struct {
			CreatedAt time.Time `json:"created_at"`
			CreatedBy string    `json:"created_by"`
			Version   int64     `json:"version"`
		}{}
	}
	return out, nil
}

// GetProfile returns a profile with its YAML, template, versions, and maintainers.
func (a *API) GetProfile(ctx context.Context, req api.GetProfileRequestObject) (api.GetProfileResponseObject, error) {
	d, err := a.detail(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return api.GetProfile200JSONResponse(d), nil
}

// UpdateProfile saves a new version (REQ-012, REQ-013).
func (a *API) UpdateProfile(ctx context.Context, req api.UpdateProfileRequestObject) (api.UpdateProfileResponseObject, error) {
	if _, err := a.profileRow(ctx, req.Key); err != nil {
		return nil, err
	}
	if _, err := a.Registry.Save(ctx, req.Key, []byte(req.Body.Yaml), []byte(req.Body.Template), actorID(ctx)); err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return api.UpdateProfile200JSONResponse(d), nil
}

// CreateProfile adds a profile for a new doc type.
func (a *API) CreateProfile(ctx context.Context, req api.CreateProfileRequestObject) (api.CreateProfileResponseObject, error) {
	if _, err := a.profileRow(ctx, req.Body.Key); err == nil {
		return nil, kernel.Conflict("profile_exists", "A profile with the key %s exists already.", req.Body.Key)
	}
	if _, err := a.Registry.Save(ctx, req.Body.Key, []byte(req.Body.Yaml), []byte(req.Body.Template), actorID(ctx)); err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.Body.Key)
	if err != nil {
		return nil, err
	}
	return api.CreateProfile200JSONResponse(d), nil
}

// SetMaintainers replaces the maintainers of a profile.
func (a *API) SetMaintainers(ctx context.Context, req api.SetMaintainersRequestObject) (api.SetMaintainersResponseObject, error) {
	row, err := a.profileRow(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	err = a.Registry.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if err := q.DeleteProfileMaintainers(ctx, row.ID); err != nil {
			return err
		}
		for _, u := range req.Body.UserIds {
			if err := q.InsertProfileMaintainer(ctx, pgdb.InsertProfileMaintainerParams{ProfileID: row.ID, UserID: u}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return api.SetMaintainers200JSONResponse(d), nil
}

func actorID(ctx context.Context) string {
	if id := kernel.ActorFrom(ctx).UserID; id != "" {
		return id
	}
	return "local"
}
