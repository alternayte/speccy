package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verify"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
)

// openThreads opens one blocking thread for each MUST outcome that blocks. The run computes
// no Build Ready verdict: the thread does, through the rule that already exists.
func (a *API) openThreads(ctx context.Context, b pgdb.SpecDoc, p profile.Profile, main []byte,
	run Run, handoff *uuid.UUID, version int64) ([]uuid.UUID, error) {

	if a.Threads == nil {
		return nil, nil
	}
	doc := section.Parse(main)
	defs := map[string]lint.Definition{}
	for _, d := range lint.Definitions(main, p.Trace.Prefixes) {
		if _, ok := defs[d.ID]; !ok {
			defs[d.ID] = d
		}
	}
	var out []uuid.UUID
	for _, o := range run.Results {
		if !o.Blocks {
			continue
		}
		an := anchor.New(b.DocPath, main, doc, doc.BodyStart, doc.BodyStart)
		if d, ok := defs[o.ID]; ok {
			an = anchor.New(b.DocPath, main, doc, d.Start, d.End)
		}
		blocking := true
		addressed := api.OpenThreadAddressedToHumans
		title := fmt.Sprintf("%s is %s in %s", o.ID, o.Outcome, run.Repo)
		in := api.OpenThread{
			AnchorKind: api.OpenThreadAnchorKindText, Anchor: toMap(an), AddressedTo: addressed,
			Blocking: &blocking, Title: &title, Body: threadBody(o, run),
		}
		d, err := a.Threads.OpenFromVerification(ctx, b.ID, handoff, version, in)
		if err != nil {
			return nil, err
		}
		out = append(out, d.Id)
	}
	return out, nil
}

// threadBody says what the gate found, and what a person must answer.
func threadBody(o Outcome, run Run) string {
	var b strings.Builder
	at := run.SHA
	if at == "" {
		at = run.Repo
	}
	switch o.Outcome {
	case verify.Missing:
		fmt.Fprintf(&b, "Speccy verified %s against %s at %s, and found no code that holds it.\n\n", o.ID, run.Repo, at)
		b.WriteString("Either the requirement is not built, or the code is there and nothing names it. ")
		b.WriteString("Answer with where it is, or say the build does not cover it yet.")
	case verify.Breached:
		fmt.Fprintf(&b, "Speccy verified %s against %s at %s. Two judges agreed that the cited code contradicts it.\n\n", o.ID, run.Repo, at)
		if o.Judgement.RequirementQuote != "" {
			b.WriteString("The requirement says:\n> " + o.Judgement.RequirementQuote + "\n\n")
		}
		if o.Judgement.CodeQuote != "" {
			b.WriteString("The code says:\n> " + o.Judgement.CodeQuote + "\n\n")
		}
		if o.Judgement.Reason != "" {
			b.WriteString(o.Judgement.Reason + "\n\n")
		}
		b.WriteString("Change the code, or change the requirement.")
	}
	for _, t := range o.Targets {
		if t.Holds {
			fmt.Fprintf(&b, "\n\nCited: %s:%d", t.Path, t.Line)
		}
	}
	return b.String()
}

func toMap(an anchor.Anchor) map[string]any {
	raw, _ := json.Marshal(an)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
