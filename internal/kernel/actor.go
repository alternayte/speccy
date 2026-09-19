package kernel

import (
	"context"

	"github.com/google/uuid"
)

// Workspace roles (SDD §3). Local mode's one user is an admin.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Actor is who makes a request: the local user, a signed-in user, or a guest on one
// bundle's share link (REQ-086).
type Actor struct {
	// UserID is the auth-all user ID, "local" in local mode, and "" for a guest.
	UserID string
	Email  string
	Role   string // admin | member; "" for a guest
	// Guest is set for a share-link guest: the bundle they may read, and their name.
	Guest *Guest
	// APIKey is true when an API token authenticated the request.
	APIKey bool
}

// Guest is a share-link visitor with a display name (REQ-086).
type Guest struct {
	ID       uuid.UUID
	BundleID uuid.UUID
	Name     string
}

// LocalActor is local mode's one implicit user with every permission (DEC-015).
var LocalActor = Actor{UserID: "local", Role: RoleAdmin}

type actorKey struct{}

// WithActor returns ctx with the actor of the request.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor of the request. A context with no actor is anonymous: it has
// no user and no role, so it passes no permission check. Local mode sets LocalActor on every
// request.
func ActorFrom(ctx context.Context) Actor {
	a, _ := ctx.Value(actorKey{}).(Actor)
	return a
}

// Anonymous reports whether the actor is nobody: no user and no guest.
func (a Actor) Anonymous() bool { return a.UserID == "" && a.Guest == nil }

// IsAdmin reports whether the actor has the admin role.
func (a Actor) IsAdmin() bool { return a.Guest == nil && a.Role == RoleAdmin }

// Person is a member of the workspace.
type Person struct {
	ID    string
	Name  string
	Email string
	Role  string
}

// Label is how the UI names a person: the name, else the email.
func (p Person) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Email
}

// Directory lists the people of the workspace. Hosted mode reads auth-all's users; local mode
// has one person.
type Directory interface {
	People(ctx context.Context) ([]Person, error)
}

// LocalDirectory is local mode's one person.
type LocalDirectory struct{}

func (LocalDirectory) People(context.Context) ([]Person, error) {
	return []Person{{ID: LocalActor.UserID, Name: "You", Role: RoleAdmin}}, nil
}

// PersonByID finds a person in a directory; an unknown ID gives a Person with the ID as its name.
func PersonByID(ctx context.Context, d Directory, id string) Person {
	if d != nil {
		if ps, err := d.People(ctx); err == nil {
			for _, p := range ps {
				if p.ID == id {
					return p
				}
			}
		}
	}
	return Person{ID: id, Name: id}
}
