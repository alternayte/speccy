package source

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SidecarDir holds one sidecar for each doc (DEC-009).
const SidecarDir = ".speccy/decisions"

// SidecarPath is the sidecar of the doc at docPath. The doc path is the path in the repo for a
// local or GitHub bundle, and the path in the bundle for a db bundle.
func SidecarPath(docPath string) string {
	return path.Join(SidecarDir, docPath+".yaml")
}

// IsSidecar says whether p is a sidecar, so a scan and a review skip it.
func IsSidecar(p string) bool {
	return strings.HasPrefix(path.Clean(p), SidecarDir+"/")
}

// Decisions is the sidecar of one doc: its approved waivers and acknowledgements (DEC-009,
// SDD §9.3). Speccy writes no decision into a doc, because it does not own the doc's format.
type Decisions struct {
	Waivers    []Waiver    `yaml:"waivers,omitempty"`
	Trace      []TraceAck  `yaml:"trace,omitempty"`
	Standalone *Standalone `yaml:"standalone,omitempty"`
	// UpstreamChanges are the upstream requests of the doc: conflicts with a linked doc that
	// the linked doc must fix.
	UpstreamChanges []UpstreamChange `yaml:"upstream_changes,omitempty"`
}

// Empty says whether the sidecar holds no decision.
func (d Decisions) Empty() bool {
	return len(d.Waivers) == 0 && len(d.Trace) == 0 && d.Standalone == nil && len(d.UpstreamChanges) == 0
}

// UpstreamChange is an upstream request: the answer "The linked doc must change" to one
// conflict of coherence.contradiction. While it is open, the conflict waits on the linked doc,
// and the review of the linked doc reports it as coherence.downstream-request.
type UpstreamChange struct {
	Conflict    Conflict `yaml:"conflict"`
	Reason      string   `yaml:"reason"`
	RequestedBy string   `yaml:"requested_by,omitempty"`
	// SentBack is the answer of the linked doc: "The downstream doc must change". It ends the
	// request, and the conflict counts again. The entry stays, so the history shows why.
	SentBack *SentBack `yaml:"sent_back,omitempty"`
}

// SentBack is a send-back: the linked doc's answer that this doc must change.
type SentBack struct {
	Reason string `yaml:"reason"`
	By     string `yaml:"by,omitempty"`
}

// Open reports whether the request still waits on the linked doc.
func (u UpstreamChange) Open() bool { return u.SentBack == nil && strings.TrimSpace(u.Reason) != "" }

// UpstreamChangeFor returns the upstream request of the conflict c, or nil.
func (d Decisions) UpstreamChangeFor(c *Conflict) *UpstreamChange {
	if c == nil {
		return nil
	}
	for i := range d.UpstreamChanges {
		if d.UpstreamChanges[i].Conflict.Key() == c.Key() {
			return &d.UpstreamChanges[i]
		}
	}
	return nil
}

// WithUpstreamChange returns the sidecar with u in it. A request for the same conflict is
// replaced, so one conflict has one upstream request.
func (d Decisions) WithUpstreamChange(u UpstreamChange) Decisions {
	out := d
	out.UpstreamChanges = slices.Clone(d.UpstreamChanges)
	for i, old := range out.UpstreamChanges {
		if old.Conflict.Key() == u.Conflict.Key() {
			out.UpstreamChanges[i] = u
			return out
		}
	}
	out.UpstreamChanges = append(out.UpstreamChanges, u)
	return out
}

// ParseDecisions parses a sidecar. An unknown key is an error, so a typo does not pass silently.
func ParseDecisions(src []byte) (Decisions, error) {
	var d Decisions
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return Decisions{}, fmt.Errorf("the sidecar does not parse: %w", err)
	}
	return d, nil
}

// Marshal writes the sidecar. An empty sidecar writes an empty file.
func (d Decisions) Marshal() ([]byte, error) {
	if d.Empty() {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WithWaiver returns the sidecar with w in it. A waiver for the same check, section and
// conflict is replaced, so one section holds one decision for one check, or for one conflict
// of coherence.contradiction.
func (d Decisions) WithWaiver(w Waiver) Decisions {
	out := d
	out.Waivers = slices.Clone(d.Waivers)
	for i, old := range out.Waivers {
		if old.Check == w.Check && slices.Equal(old.Section, w.Section) && old.Conflict.Same(w.Conflict) {
			out.Waivers[i] = w
			return out
		}
	}
	out.Waivers = append(out.Waivers, w)
	return out
}

// ContradictionCheck is the coherence check whose finding names one conflict with a linked doc.
const ContradictionCheck = "coherence.contradiction"

// DownstreamRequestCheck is the finding of a linked doc for an open upstream request: a doc that
// links to it says that it must change.
const DownstreamRequestCheck = "coherence.downstream-request"

// Conflict is one conflict between a doc and a linked doc: what a waiver of
// coherence.contradiction excuses (#136).
type Conflict struct {
	// With is the slug of the linked doc.
	With string `yaml:"with" json:"with"`
	// Quote is the text of this doc, and WithQuote the text of the linked doc.
	Quote     string `yaml:"quote" json:"quote"`
	WithQuote string `yaml:"with_quote" json:"with_quote"`
}

// Same reports whether c and o name the same conflict. Two absent conflicts are the same. The
// quotes match with case and runs of white space folded, because a model quotes the same text
// with such small changes from one run to the next. A conflict quoted in other words is a new
// conflict, and it needs its own waiver.
func (c *Conflict) Same(o *Conflict) bool {
	if c == nil || o == nil {
		return c == nil && o == nil
	}
	return c.Key() == o.Key()
}

// Key is the folded identity of the conflict. An absent conflict has the empty key.
func (c *Conflict) Key() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.With) + "\x00" + foldQuote(c.Quote) + "\x00" + foldQuote(c.WithQuote)
}

func foldQuote(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// ConflictOf reads the conflict from the evidence of a finding. It is nil for a finding of
// another check, and for evidence that names no conflict. The conflict of a
// coherence.downstream-request finding is the conflict as the downstream doc names it.
func ConflictOf(check string, evidence []byte) *Conflict {
	if check == DownstreamRequestCheck {
		var ev struct {
			Conflict *Conflict `json:"conflict"`
		}
		if json.Unmarshal(evidence, &ev) != nil || ev.Conflict == nil || strings.TrimSpace(ev.Conflict.Quote) == "" || strings.TrimSpace(ev.Conflict.WithQuote) == "" {
			return nil
		}
		return ev.Conflict
	}
	if check != ContradictionCheck {
		return nil
	}
	var ev struct {
		Upstream      string `json:"upstream"`
		Quote         string `json:"quote"`
		UpstreamQuote string `json:"upstream_quote"`
	}
	if json.Unmarshal(evidence, &ev) != nil || ev.Upstream == "" || strings.TrimSpace(ev.Quote) == "" || strings.TrimSpace(ev.UpstreamQuote) == "" {
		return nil
	}
	return &Conflict{With: ev.Upstream, Quote: ev.Quote, WithQuote: ev.UpstreamQuote}
}

// WithTraceAck returns the sidecar with t in it, replacing the acknowledgement of the same ID.
func (d Decisions) WithTraceAck(t TraceAck) Decisions {
	out := d
	out.Trace = slices.Clone(d.Trace)
	for i, old := range out.Trace {
		if old.ID == t.ID {
			out.Trace[i] = t
			return out
		}
	}
	out.Trace = append(out.Trace, t)
	return out
}

// WithoutTraceAck returns the sidecar without the acknowledgement of id, and whether it held one.
func (d Decisions) WithoutTraceAck(id string) (Decisions, bool) {
	out := d
	out.Trace = slices.DeleteFunc(slices.Clone(d.Trace), func(t TraceAck) bool { return t.ID == id })
	return out, len(out.Trace) != len(d.Trace)
}
