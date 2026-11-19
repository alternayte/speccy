package kernel

import "context"

// The queues of the work that calls models (docs/specs/hosted-operations.md). Each has its own
// monthly budget.
const (
	QueueInteractive = "interactive"
	QueuePR          = "pr"
)

type queueKey struct{}

type repoKey struct{}

// WithQueue returns ctx for work of the queue: its model calls spend that queue's budget.
func WithQueue(ctx context.Context, queue string) context.Context {
	return context.WithValue(ctx, queueKey{}, queue)
}

// QueueFrom returns the queue of the work. Work outside a queue, such as a call a person makes
// in the app, is interactive.
func QueueFrom(ctx context.Context) string {
	if q, _ := ctx.Value(queueKey{}).(string); q != "" {
		return q
	}
	return QueueInteractive
}

// WithRepo returns ctx for work on a GitHub repo, as owner/name, so its spend names the repo.
func WithRepo(ctx context.Context, repo string) context.Context {
	return context.WithValue(ctx, repoKey{}, repo)
}

// RepoFrom returns the repo of the work, or "".
func RepoFrom(ctx context.Context) string {
	r, _ := ctx.Value(repoKey{}).(string)
	return r
}
