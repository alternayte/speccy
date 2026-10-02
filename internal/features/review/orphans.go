package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
)

// StoppedCause is the error of a run whose process stopped before the run ended.
const StoppedCause = "The process that ran this review stopped before the review ended. Run the review again."

// EndOrphans ends each job that it finds queued or running, and the run of each one. The
// owner of a local state calls it when it starts: one process owns the state, so a job that
// is not in this process's own memory has no process behind it. Its run would otherwise stay
// active until the job lock ran out, and refuse every new review of the doc until then.
//
// Orphaned ends the run of a job of another feature, by job kind.
func (s *Service) EndOrphans(ctx context.Context, orphaned map[string]func(ctx context.Context, payload []byte) error) error {
	q := s.DB.Queries()
	jobs, err := q.ListActiveJobs(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, job := range jobs {
		if end, ok := orphaned[job.Kind]; ok {
			if err := end(ctx, job.Payload); err != nil {
				return err
			}
		} else if job.Kind == jobKindRun {
			var p runJob
			if json.Unmarshal(job.Payload, &p) == nil && p.RunID != "" {
				if err := q.FailActiveRun(ctx, pgdb.FailActiveRunParams{ID: uuidOf(p.RunID), Error: StoppedCause,
					FinishedAt: sql.NullTime{Time: now, Valid: true}}); err != nil {
					return err
				}
			}
		}
		if err := q.FinishJob(ctx, pgdb.FinishJobParams{ID: job.ID, Status: "failed", LastError: StoppedCause}); err != nil {
			return err
		}
	}
	return nil
}

// Idle reports whether no job waits for the worker or runs in it. An owner that wants to exit
// waits for it, so a job that a client process queued does not fail because the owner ended.
func (s *Service) Idle(ctx context.Context) (bool, error) {
	jobs, err := s.DB.Queries().ListActiveJobs(ctx)
	return len(jobs) == 0, err
}
