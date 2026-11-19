// Package notify posts the workspace's notifications to one Slack or Teams channel
// (docs/specs/channel-notifications.md). Each message is a job, so a slow or failed webhook
// never blocks the action that caused it.
package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/store"
)

// JobKind is the job kind of one channel message.
const JobKind = "notify"

// The webhook kinds.
const (
	Slack = "slack"
	Teams = "teams"
)

// Env is the webhook that SPECCY_NOTIFY_WEBHOOK_URL and SPECCY_NOTIFY_WEBHOOK_KIND set. It
// wins over the stored one and locks the UI.
type Env struct {
	Kind string
	URL  string
}

// API is the channel webhook and its messages.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Sealer    *kernel.Sealer
	Queue     *queue.Queue
	People    kernel.Directory
	// BaseURL is SPECCY_BASE_URL, for the links. Empty in local mode, which posts nothing.
	BaseURL string
	Env     *Env
	HTTP    *http.Client
}

// Register registers the message job: 1 try and 3 retries, then the failure is logged.
func (a *API) Register() {
	a.Queue.Register(JobKind, queue.Interactive, 4, a.handle, nil)
}

// message is the payload of one job.
type message struct {
	Text  string `json:"text"`
	Title string `json:"title,omitempty"`
	Link  string `json:"link,omitempty"`
}

// Notice is one thing to tell the channel.
type Notice struct {
	// Doc is the spec doc the notice is about, or uuid.Nil for a workspace notice. A notice
	// about a doc of a Private bundle posts nothing.
	Doc uuid.UUID
	// Actor is the user ID of the person who did it, named first; empty for none.
	Actor string
	// Event is the sentence after the actor's name, such as "asks for a review.", or the
	// whole sentence when there is no actor.
	Event string
	// People are the user IDs the notice waits for, named in the message.
	People []string
}

// Post queues the message of a notice. It does nothing when no webhook is set, in local
// mode, or for a Private bundle. A failure to queue is logged: a notice never fails the
// action that caused it.
func (a *API) Post(ctx context.Context, n Notice) {
	if a == nil || a.BaseURL == "" {
		return
	}
	if err := a.post(ctx, n); err != nil {
		slog.WarnContext(ctx, "a channel notification was not queued", "event", n.Event, "err", err)
	}
}

func (a *API) post(ctx context.Context, n Notice) error {
	if _, ok, err := a.webhook(ctx); err != nil || !ok {
		return err
	}
	m := message{Text: n.Event}
	if n.Actor != "" {
		if name := a.name(ctx, n.Actor); name != "" {
			m.Text = name + " " + n.Event
		}
	}
	if n.Doc != uuid.Nil {
		q := a.DB.Queries()
		d, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: n.Doc})
		if err != nil {
			return err
		}
		b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: d.BundleID})
		if err != nil {
			return err
		}
		if b.Visibility == "private" {
			return nil // the channel cannot see who may read the bundle
		}
		m.Title, m.Link = d.Title, a.BaseURL+"/bundles/"+b.ID.String()
	}
	if names := a.names(ctx, n.People); names != "" {
		m.Text += " " + names + "."
	}
	_, err := a.Queue.Insert(ctx, nil, JobKind, m, nil)
	return err
}

// name is the name of one person, or "".
func (a *API) name(ctx context.Context, id string) string {
	if a.People == nil {
		return ""
	}
	people, err := a.People.People(ctx)
	if err != nil {
		return ""
	}
	for _, p := range people {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

// names joins the names of the people, as "For Kim and Lee".
func (a *API) names(ctx context.Context, ids []string) string {
	if len(ids) == 0 || a.People == nil {
		return ""
	}
	people, err := a.People.People(ctx)
	if err != nil {
		return ""
	}
	byID := map[string]string{}
	for _, p := range people {
		byID[p.ID] = p.Name
	}
	var names []string
	for _, id := range ids {
		if n := byID[id]; n != "" {
			names = append(names, n)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "For " + names[0]
	}
	return "For " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (a *API) handle(ctx context.Context, payload []byte) error {
	var m message
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil //nolint:nilerr // a payload that does not parse is no message
	}
	w, ok, err := a.webhook(ctx)
	if err != nil || !ok {
		return err
	}
	status, err := a.send(ctx, w, m)
	if err != nil {
		slog.WarnContext(ctx, "the channel webhook failed", "status", status, "err", err)
	}
	return err
}

type hook struct {
	kind, url string
}

// webhook is the webhook to post to: the env one, else the stored one. ok is false with none.
func (a *API) webhook(ctx context.Context) (hook, bool, error) {
	if a.Env != nil {
		return hook{kind: a.Env.Kind, url: a.Env.URL}, true, nil
	}
	row, err := a.DB.Queries().GetNotifyWebhook(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return hook{}, false, nil
	}
	if err != nil {
		return hook{}, false, err
	}
	raw, err := a.Sealer.Open(row.UrlEncrypted)
	if errors.Is(err, kernel.ErrOtherKey) {
		return hook{}, false, kernel.OtherKey("The channel webhook", "Admin → Notifications")
	}
	if err != nil {
		return hook{}, false, err
	}
	return hook{kind: row.Kind, url: string(raw)}, true, nil
}

// send posts one message and returns the HTTP status.
func (a *API) send(ctx context.Context, w hook, m message) (int, error) {
	body, err := json.Marshal(payload(w.kind, m))
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	c := a.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode/100 != 2 {
		return res.StatusCode, fmt.Errorf("the webhook answered %d", res.StatusCode)
	}
	return res.StatusCode, nil
}

// payload is the body of one message in the kind's own format.
func payload(kind string, m message) any {
	if kind == Teams {
		// A Teams Workflows webhook takes a message with an Adaptive Card attachment.
		body := []map[string]any{}
		if m.Title != "" {
			body = append(body, map[string]any{"type": "TextBlock", "text": m.Title, "weight": "Bolder", "wrap": true})
		}
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Text, "wrap": true})
		card := map[string]any{"type": "AdaptiveCard", "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
			"version": "1.4", "body": body}
		if m.Link != "" {
			card["actions"] = []map[string]any{{"type": "Action.OpenUrl", "title": "Open in Speccy", "url": m.Link}}
		}
		return map[string]any{"type": "message", "attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive", "content": card}}}
	}
	text := m.Text
	if m.Link != "" {
		title := m.Title
		if title == "" {
			title = "Open in Speccy"
		}
		text = "<" + m.Link + "|" + slackEscape(title) + ">: " + slackEscape(m.Text)
	}
	return map[string]string{"text": text}
}

// slackEscape escapes the three characters Slack's text format reserves.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var errLocal = kernel.Invalid("hosted_only", "Notifications go to a channel from a hosted Speccy. Local mode has the inbox.")

// GetNotifications is GET /admin/notifications.
func (a *API) GetNotifications(ctx context.Context, _ api.GetNotificationsRequestObject) (api.GetNotificationsResponseObject, error) {
	if a.BaseURL == "" {
		return nil, errLocal
	}
	out, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetNotifications200JSONResponse(out), nil
}

func (a *API) state(ctx context.Context) (api.NotifyWebhook, error) {
	if a.Env != nil {
		kind, last4 := api.NotifyWebhookKind(a.Env.Kind), lastFour(a.Env.URL)
		return api.NotifyWebhook{Configured: true, FromEnv: true, Kind: &kind, UrlLast4: &last4}, nil
	}
	row, err := a.DB.Queries().GetNotifyWebhook(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return api.NotifyWebhook{}, nil
	}
	if err != nil {
		return api.NotifyWebhook{}, err
	}
	kind := api.NotifyWebhookKind(row.Kind)
	return api.NotifyWebhook{Configured: true, Kind: &kind, UrlLast4: &row.UrlLast4}, nil
}

// SetNotifications is PUT /admin/notifications.
func (a *API) SetNotifications(ctx context.Context, req api.SetNotificationsRequestObject) (api.SetNotificationsResponseObject, error) {
	if a.BaseURL == "" {
		return nil, errLocal
	}
	if a.Env != nil {
		return nil, kernel.Invalid("from_env", "SPECCY_NOTIFY_WEBHOOK_URL sets the webhook. Change it there.")
	}
	raw := strings.TrimSpace(req.Body.Url)
	if err := ValidURL(raw); err != nil {
		return nil, err
	}
	sealed, err := a.Sealer.Seal([]byte(raw))
	if err != nil {
		return nil, err
	}
	if err := a.DB.Queries().SetNotifyWebhook(ctx, pgdb.SetNotifyWebhookParams{WorkspaceID: a.Workspace, Kind: string(req.Body.Kind),
		UrlEncrypted: sealed, UrlLast4: lastFour(raw), UpdatedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	out, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return api.SetNotifications200JSONResponse(out), nil
}

// DeleteNotifications is DELETE /admin/notifications.
func (a *API) DeleteNotifications(ctx context.Context, _ api.DeleteNotificationsRequestObject) (api.DeleteNotificationsResponseObject, error) {
	if a.Env != nil {
		return nil, kernel.Invalid("from_env", "SPECCY_NOTIFY_WEBHOOK_URL sets the webhook. Change it there.")
	}
	if err := a.DB.Queries().DeleteNotifyWebhook(ctx, a.Workspace); err != nil {
		return nil, err
	}
	return api.DeleteNotifications204Response{}, nil
}

// TestNotifications is POST /admin/notifications/test: one message, at once, not as a job.
func (a *API) TestNotifications(ctx context.Context, _ api.TestNotificationsRequestObject) (api.TestNotificationsResponseObject, error) {
	if a.BaseURL == "" {
		return nil, errLocal
	}
	w, ok, err := a.webhook(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, kernel.Invalid("no_webhook", "Set a webhook first.")
	}
	status, err := a.send(ctx, w, message{Text: "This is a test message from Speccy. Notifications reach this channel.", Title: "Speccy", Link: a.BaseURL})
	out := api.TestNotifications200JSONResponse{Status: status}
	if err != nil {
		msg := err.Error()
		out.Error = &msg
	}
	return out, nil
}

// ValidURL checks a webhook URL: https, with a host.
func ValidURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return kernel.Invalid("bad_url", "The webhook URL must be an https URL, as Slack or Teams gives it.")
	}
	return nil
}

func lastFour(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}
