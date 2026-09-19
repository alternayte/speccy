package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/app"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// operation is one operation of api/openapi.yaml.
type operation struct {
	id, method, path string
}

func readOperations(t *testing.T) []operation {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []operation
	for path, item := range doc.Paths {
		for method, op := range item {
			m, ok := op.(map[string]any)
			if !ok || m["operationId"] == nil {
				continue
			}
			out = append(out, operation{id: m["operationId"].(string), method: strings.ToUpper(method), path: path})
		}
	}
	return out
}

const mainDoc = "---\ntype: sdd\ntitle: Pay\n---\n\n# Pay\n\nThe service retries.\n"

// T-041
func TestAuthz_EndpointRoleTable(t *testing.T) {
	ops := readOperations(t)
	table := speccyhttp.Operations()
	// Every operation has a row, and every row names an operation.
	seen := map[string]bool{}
	for _, op := range ops {
		seen[op.id] = true
		if _, ok := table[op.id]; !ok {
			t.Errorf("%s has no row in the role table", op.id)
		}
	}
	for id := range table {
		if !seen[id] {
			t.Errorf("the role table has a row for %s, which is not in api/openapi.yaml", id)
		}
	}

	actors := []struct {
		name  string
		actor func(bundle uuid.UUID) kernel.Actor
	}{
		{"anonymous", func(uuid.UUID) kernel.Actor { return kernel.Actor{} }},
		{"guest", func(b uuid.UUID) kernel.Actor {
			return kernel.Actor{Guest: &kernel.Guest{ID: uuid.New(), BundleID: b, Name: "Guest"}}
		}},
		{"member", func(uuid.UUID) kernel.Actor { return kernel.Actor{UserID: "user-member", Role: kernel.RoleMember} }},
		{"author", func(uuid.UUID) kernel.Actor { return kernel.Actor{UserID: "user-author", Role: kernel.RoleMember} }},
		{"admin", func(uuid.UUID) kernel.Actor { return kernel.Actor{UserID: "user-admin", Role: kernel.RoleAdmin} }},
	}
	allowed := map[int][]string{
		speccyhttp.Public:     {"anonymous", "guest", "member", "author", "admin"},
		speccyhttp.Reader:     {"guest", "member", "author", "admin"},
		speccyhttp.Member:     {"member", "author", "admin"},
		speccyhttp.BundleRead: {"guest", "member", "author", "admin"},
		speccyhttp.BundleAI:   {"member", "author", "admin"},
		speccyhttp.BundleEdit: {"author", "admin"},
		speccyhttp.AdminOnly:  {"admin"},
		// The fixture has no maintainers, so only the admin passes.
		speccyhttp.ProfileEdit: {"admin"},
		speccyhttp.Maintainer:  {"admin"},
	}

	e := storetest.Engines()[0]
	for _, op := range ops {
		t.Run(op.id, func(t *testing.T) {
			// A fresh workspace per operation: a bundle with link visibility, one author, and a
			// finished lint run.
			env := newHosted(t, e)
			for _, a := range actors {
				want := false
				for _, n := range allowed[table[op.id]] {
					want = want || n == a.name
				}
				status, code := env.call(t, op, a.actor(env.bundle.ID))
				denied := code == "sign_in_required" || code == "forbidden" || code == "guest_not_allowed" || code == "not_author" || code == "not_maintainer" ||
					(status == 404 && code == "bundle_not_found")
				if denied == want {
					t.Errorf("%s as %s: status %d, code %q; want allowed %v", op.id, a.name, status, code, want)
				}
			}
		})
	}
}

// A member who is not an author cannot see a private bundle; an admin can (REQ-084).
func TestAuthz_PrivateBundle(t *testing.T) {
	env := newHosted(t, storetest.Engines()[0])
	if err := env.app.Bundles.DB.Queries().SetBundleVisibility(context.Background(), pgdb.SetBundleVisibilityParams{ID: env.bundle.ID, Visibility: "private", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	get := operation{id: "getBundle", method: "GET", path: "/bundles/{bundleId}"}
	list := operation{id: "listBundles", method: "GET", path: "/bundles"}
	for _, c := range []struct {
		actor  kernel.Actor
		status int
		listed int
	}{
		{kernel.Actor{UserID: "user-member", Role: kernel.RoleMember}, 404, 0},
		{kernel.Actor{UserID: "user-author", Role: kernel.RoleMember}, 200, 1},
		{kernel.Actor{UserID: "user-admin", Role: kernel.RoleAdmin}, 200, 1},
	} {
		if status, _ := env.call(t, get, c.actor); status != c.status {
			t.Errorf("%s: getBundle %d, want %d", c.actor.UserID, status, c.status)
		}
		_, body := env.do(t, list, c.actor)
		var l struct{ Items []any }
		_ = json.Unmarshal(body, &l)
		if len(l.Items) != c.listed {
			t.Errorf("%s: listBundles shows %d bundles, want %d", c.actor.UserID, len(l.Items), c.listed)
		}
	}
}

type hostedEnv struct {
	app     *app.App
	handler nethttp.Handler
	bundle  pgdb.Bundle
	run     pgdb.ReviewRun
	thread  string
	waiver  string
	finding string
}

type actorKey struct{}

func newHosted(t *testing.T, e storetest.Engine) *hostedEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := e.Open(t)
	a, err := app.New(ctx, db, nil, app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.API.Hosted = true
	b, err := a.Bundles.CreateDB(ctx, "pay", []source.File{{Path: "SPEC.md", Content: []byte(mainDoc)}}, "user-author")
	if err != nil {
		t.Fatal(err)
	}
	q := db.Queries()
	if err := q.SetBundleVisibility(ctx, pgdb.SetBundleVisibilityParams{ID: b.ID, Visibility: "link", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	run, err := q.LatestRun(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor := func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a, _ := r.Context().Value(actorKey{}).(kernel.Actor)
			next.ServeHTTP(w, r.WithContext(kernel.WithActor(r.Context(), a)))
		})
	}
	spa := fs.FS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})
	h := speccyhttp.Handler(spa, a.API, speccyhttp.Options{Actor: actor, Authz: &speccyhttp.Authz{DB: db, Workspace: a.Workspace}})
	env := &hostedEnv{app: a, handler: h, bundle: b, run: run}
	// A thread and a waiver request, made by the admin, for the thread and waiver paths.
	admin := kernel.Actor{UserID: "user-admin", Role: kernel.RoleAdmin}
	_, body := env.post(t, "/bundles/"+b.ID.String()+"/threads", `{"anchor_kind":"section","anchor":{"heading_path":[]},"addressed_to":"humans","body":"Who owns retries?"}`, admin)
	var th struct{ ID string }
	_ = json.Unmarshal(body, &th)
	env.thread = th.ID
	fs, err := q.ListFindings(ctx, run.ID)
	if err != nil || len(fs) == 0 {
		t.Fatalf("the fixture run has no finding to waive: %v", err)
	}
	_, body = env.post(t, "/bundles/"+b.ID.String()+"/waivers", `{"finding_id":"`+fs[0].ID.String()+`","reason":"The provider owns this part of the design."}`, admin)
	var w struct{ ID string }
	_ = json.Unmarshal(body, &w)
	env.waiver = w.ID
	env.finding = fs[0].ID.String()
	if env.thread == "" || env.waiver == "" {
		t.Fatalf("fixture thread %q, waiver %q", env.thread, env.waiver)
	}
	return env
}

func (env *hostedEnv) post(t *testing.T, path, body string, a kernel.Actor) (int, []byte) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.WithValue(context.Background(), actorKey{}, a), "POST", "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// do calls op as the actor, with the fixture's IDs in the path and minimal parameters.
func (env *hostedEnv) do(t *testing.T, op operation, a kernel.Actor) (int, []byte) {
	t.Helper()
	path := strings.NewReplacer(
		"{bundleId}", env.bundle.ID.String(), "{runId}", env.run.ID.String(), "{token}", "not-a-token",
		"{connectionId}", uuid.NewString(), "{backendId}", uuid.NewString(), "{inviteId}", uuid.NewString(), "{role}", "reviewer",
		"{threadId}", env.thread, "{waiverId}", env.waiver, "{key}", "sdd", "{findingId}", env.finding, "{sourceId}", uuid.NewString(),
	).Replace(op.path)
	query := "?path=SPEC.md&base_version=" + env.bundle.CurrentVersionID.UUID.String() +
		"&from=" + env.bundle.CurrentVersionID.UUID.String() + "&to=" + env.bundle.CurrentVersionID.UUID.String()
	var body *bytes.Reader
	switch op.method {
	case "POST", "PUT":
		body = bytes.NewReader([]byte("{}"))
	default:
		body = bytes.NewReader(nil)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), actorKey{}, a), 5*time.Second)
	defer cancel()
	contentType := "application/json"
	switch op.id {
	case "putFileContent":
		contentType = "application/octet-stream"
	case "importBundle":
		// A valid form, so the body decodes and the role check decides.
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "SPEC.md")
		_, _ = fw.Write([]byte(mainDoc))
		_ = mw.Close()
		body, contentType = bytes.NewReader(buf.Bytes()), mw.FormDataContentType()
	}
	req := httptest.NewRequestWithContext(ctx, op.method, "/api/v1"+path+query, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (env *hostedEnv) call(t *testing.T, op operation, a kernel.Actor) (int, string) {
	status, body := env.do(t, op, a)
	var p struct{ Code string }
	_ = json.Unmarshal(body, &p)
	return status, p.Code
}
