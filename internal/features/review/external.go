package review

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// CodeDriftSlug is the check that a code target has not changed since the doc version
// (DEC-021). It warns: the code moving is news, not a defect in the doc.
const CodeDriftSlug = "links.code-drift"

// Link states.
const (
	stateAligned     = "aligned"
	stateDrifted     = "drifted"
	stateConflicting = "conflicting"
	stateUnchecked   = "unchecked"
)

// externalState is what a run read about one external link.
type externalState struct {
	ref     string
	state   string
	reason  string
	checked string // the commit of a code target, or the content hash of a page
}

// driftStage reads each external link with the credential Speccy already holds, and reports a
// code target that changed after the doc version. A target it cannot read is unchecked.
func (s *Service) driftStage(ctx context.Context, rc *runCtx, in input, ev *evaluation) error {
	var external []link
	for _, l := range in.links {
		if l.targetKind == "external" && l.problem == "" && l.external != nil {
			external = append(external, l)
		}
	}
	if len(external) == 0 {
		return nil
	}
	since, err := s.versionTime(ctx, in)
	if err != nil {
		return err
	}
	previous, err := s.previousStates(ctx, in.bundle.ID)
	if err != nil {
		return err
	}
	level := in.level(CodeDriftSlug, checkLevel(in.profile.Profile, CodeDriftSlug, kernel.Should))
	code := 0
	for i, l := range external {
		t := *l.external
		if rc != nil {
			rc.publish(Event{Type: "progress", Stage: StageCoherence, Message: "Reading " + t.URL, Done: i, Total: len(external)})
		}
		if t.Scheme == source.GitHubScheme && l.kind != source.ExternalKind {
			// No stage reads a code target of another kind: drift is for the code that
			// implements this doc, and the conflict stage reads pages and issues only.
			ev.setExternal(externalState{ref: l.ref, state: stateUnchecked,
				reason: "Speccy reads a github: link for drift only when its kind is " + source.ExternalKind +
					", so no review run reads this " + l.kind + " link."})
			continue
		}
		if t.Scheme != source.GitHubScheme {
			// A target that is not code is read by the conflict stage, when a connection covers
			// its host. Say which of the two is missing.
			reason := "A review run reads this link with the MCP connection for " + t.Host + "."
			if f, err := s.fetcherFor(ctx, t.Host); err != nil || f == nil {
				reason = "No MCP connection reads " + t.Host + ". An admin adds the host in Admin → MCP connections."
			}
			ev.setExternal(externalState{ref: l.ref, state: stateUnchecked, reason: reason})
			continue
		}
		code++
		st := s.codeState(ctx, l.ref, t, since)
		ev.setExternal(st)
		if st.state != stateDrifted {
			continue
		}
		ev.findings = append(ev.findings, pending{
			slug: CodeDriftSlug, level: level, stage: StageCoherence, anchor: docAnchor(in),
			message: st.reason,
			fix:     "Read the code and say what this doc must change, or waive the drift with a reason.",
			evidence: map[string]string{"target": t.URL, "commit": st.checked,
				"compare": compareURL(t, previous[l.ref], st.checked),
				// The build a verification run reads to check the code still conforms.
				"verify": "https://" + t.Host + "/" + t.Repo + "/commit/" + st.checked},
		})
	}
	if code > 0 {
		passed := true
		for _, st := range ev.external {
			if st.state == stateDrifted {
				passed = false
			}
		}
		ev.items = append(ev.items, verdict.Item{Slug: CodeDriftSlug, Category: verdict.Coherence, Level: level, Passed: passed, Applicable: true})
	}
	return nil
}

// codeState reads the newest commit that touched the target, and compares it with since.
func (s *Service) codeState(ctx context.Context, ref string, t source.ExternalTarget, since time.Time) externalState {
	if t.Commit != "" {
		return externalState{ref: ref, state: stateAligned, checked: t.Commit,
			reason: "This target names one commit, so it does not move."}
	}
	if s.GitHub == nil {
		return externalState{ref: ref, state: stateUnchecked, reason: "Speccy has no GitHub credential for " + t.Host + "."}
	}
	client, err := s.GitHub(ctx, apiURLFor(t.Host))
	if err != nil {
		return externalState{ref: ref, state: stateUnchecked, reason: sentence(err.Error())}
	}
	sha, at, ok, err := client.LastCommit(ctx, t.Repo, "", t.Path)
	switch {
	case err != nil:
		return externalState{ref: ref, state: stateUnchecked, reason: sentence(err.Error())}
	case !ok:
		return externalState{ref: ref, state: stateUnchecked, reason: "GitHub has no commit for " + t.Repo + " at that path."}
	case at.After(since):
		return externalState{ref: ref, state: stateDrifted, checked: sha,
			reason: fmt.Sprintf("The code at %s changed after this version. The newest commit is %s, on %s.",
				t.Ref, short(sha), at.Format("2 January 2006"))}
	}
	return externalState{ref: ref, state: stateAligned, checked: sha,
		reason: "The newest commit is " + short(sha) + ", from before this version."}
}

// apiURLFor returns the API address of a GitHub host.
func apiURLFor(host string) string {
	if host == "github.com" {
		return github.DefaultAPI
	}
	return "https://" + host + "/api/v3"
}

// compareURL is the URL that shows what changed. It compares with the commit the last run
// read, and falls back to the commit itself.
func compareURL(t source.ExternalTarget, was, now string) string {
	if was != "" && was != now {
		return "https://github.com/" + t.Repo + "/compare/" + was + "..." + now
	}
	return "https://github.com/" + t.Repo + "/commit/" + now
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// versionTime returns the time the doc version was saved. Drift is measured against it,
// because the version is the record of a person reading the doc.
func (s *Service) versionTime(ctx context.Context, in input) (time.Time, error) {
	if in.version == uuid.Nil {
		return time.Now().UTC(), nil
	}
	v, err := s.DB.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: in.bundle.ID, ID: in.version})
	if err != nil {
		return time.Time{}, err
	}
	return v.CreatedAt, nil
}

// previousStates returns the commit each external link was at on the last run. The drift
// finding compares with it, so a reader sees what changed.
func (s *Service) previousStates(ctx context.Context, bundleID uuid.UUID) (map[string]string, error) {
	rows, err := s.DB.Queries().ListLinkStates(ctx, bundleID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.TargetRef] = r.CheckedRef
	}
	return out, nil
}

// storeLinkStates replaces the external link states of a bundle.
func storeLinkStates(ctx context.Context, q store.Querier, bundleID uuid.UUID, states []externalState, at time.Time) error {
	if err := q.DeleteLinkStates(ctx, bundleID); err != nil {
		return err
	}
	for _, st := range states {
		if err := q.InsertLinkState(ctx, pgdb.InsertLinkStateParams{SpecDocID: bundleID, TargetRef: st.ref,
			State: st.state, Reason: st.reason, CheckedRef: st.checked, CheckedAt: at}); err != nil {
			return err
		}
	}
	return nil
}

// ExternalConflictSlug is the check that the doc does not conflict with the issue or the page
// it links to (DEC-021). It warns: one of the two is behind, and a person decides which.
const ExternalConflictSlug = "coherence.external"

// Fetcher reads one external target through an admin MCP connection.
type Fetcher interface {
	// Name is the connection's name, for the finding and the state reason.
	Name() string
	// Fetch returns the target's text. The result is untrusted data (REQ-113).
	Fetch(ctx context.Context, target string) (string, error)
}

// conflictStage reads each external target that a connection covers, and reports statements in
// the doc that conflict with it. A target with no connection reads unchecked.
func (s *Service) conflictStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, fingerprint string) error {
	lvl := in.level(ExternalConflictSlug, checkLevel(in.profile.Profile, ExternalConflictSlug, kernel.Should))
	read, kept := 0, 0
	for _, l := range in.links {
		if l.targetKind != "external" || l.problem != "" || l.external == nil || l.external.Scheme == source.GitHubScheme {
			continue
		}
		t := *l.external
		f, err := s.fetcherFor(ctx, t.Host)
		if err != nil {
			ev.setExternal(externalState{ref: l.ref, state: stateUnchecked, reason: sentence(err.Error())})
			continue
		}
		if f == nil {
			ev.setExternal(externalState{ref: l.ref, state: stateUnchecked,
				reason: "No MCP connection reads " + t.Host + ". An admin adds the host in Admin → MCP connections."})
			continue
		}
		rc.publish(Event{Type: "progress", Stage: StageCoherence, Message: "Reading " + t.URL})
		content, err := f.Fetch(ctx, t.URL)
		if err != nil {
			ev.setExternal(externalState{ref: l.ref, state: stateUnchecked, reason: f.Name() + " could not read it: " + sentence(err.Error())})
			continue
		}
		read++
		hash := hashOf(content)
		conflicts, err := s.externalConflicts(ctx, rc, in, fingerprint, t, content, hash)
		if err != nil {
			return err
		}
		state := externalState{ref: l.ref, state: stateAligned, checked: hash,
			reason: f.Name() + " read it, and it agrees with this doc."}
		for _, c := range conflicts {
			ts, te, ok := anchor.Find(in.main, c.ThisQuote)
			if !ok {
				continue // the REQ-043 rule: a quote that is not in the doc is not evidence
			}
			kept++
			state.state = stateConflicting
			state.reason = "This doc conflicts with " + t.Ref + "."
			ev.findings = append(ev.findings, pending{
				slug: ExternalConflictSlug, level: lvl, stage: StageCoherence,
				anchor:  anchor.New(in.bundle.DocPath, in.main, in.doc, ts, te),
				message: fmt.Sprintf("This conflicts with %s: %s", t.Ref, sentence(c.Explanation)),
				fix:     fmt.Sprintf("Change this doc, or change %s, so that both say the same thing.", t.Ref),
				evidence: map[string]any{"target": t.URL, "source": f.Name(), "explanation": c.Explanation,
					"quote": c.ThisQuote, "target_quote": c.OtherQuote},
			})
		}
		ev.setExternal(state)
	}
	if read > 0 {
		ev.items = append(ev.items, verdict.Item{Slug: ExternalConflictSlug, Category: verdict.Coherence,
			Level: lvl, Passed: kept == 0, Applicable: true})
	}
	return nil
}

// fetcherFor returns the connection that reads host, or nil when none does.
func (s *Service) fetcherFor(ctx context.Context, host string) (Fetcher, error) {
	if s.Fetch == nil {
		return nil, nil
	}
	return s.Fetch(ctx, host)
}

// externalConflicts asks the reviewer for conflicts between the doc and the fetched text. The
// answer is cached on the text's hash, so an unchanged issue costs nothing on the next run.
func (s *Service) externalConflicts(ctx context.Context, rc *runCtx, in input, fingerprint string,
	t source.ExternalTarget, content, hash string) ([]conflict, error) {
	key := cacheKey{Step: "external-conflict", InputHash: hashOf(bundleHash(in), hash),
		ProfileVer: in.profile.Version, Fingerprint: fingerprint, PromptVersion: PromptContradiction, Extra: t.Scheme}
	var out struct {
		Conflicts []conflict `json:"conflicts"`
	}
	ok, err := s.cached(ctx, key, &out)
	if err != nil {
		return nil, err
	}
	if ok {
		rc.hit()
		return keptConflicts(out.Conflicts), nil
	}
	res, err := rc.call(ctx, s.Gateway, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptContradiction, System: systemPrompt,
		Prompt: contradictionPrompt("references", bundleData(in.bundle.DocPath, in.main, textAssets(in)),
			data("The linked artifact "+t.Ref, content)),
		Schema: contradictionSchema, MaxTokens: 4000,
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		return nil, err
	}
	if err := s.putCache(ctx, key, out); err != nil {
		return nil, err
	}
	return keptConflicts(out.Conflicts), nil
}

// keptConflicts drops the ones the reviewer found a builder can follow both of.
func keptConflicts(all []conflict) []conflict {
	var out []conflict
	for _, c := range all {
		if !c.BothCanHold {
			out = append(out, c)
		}
	}
	return out
}

// setExternal replaces the state of one external link, so two stages do not store it twice.
func (ev *evaluation) setExternal(st externalState) {
	for i := range ev.external {
		if ev.external[i].ref == st.ref {
			ev.external[i] = st
			return
		}
	}
	ev.external = append(ev.external, st)
}
