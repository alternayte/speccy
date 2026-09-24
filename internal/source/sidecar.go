package source

import (
	"bytes"
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
}

// Empty says whether the sidecar holds no decision.
func (d Decisions) Empty() bool {
	return len(d.Waivers) == 0 && len(d.Trace) == 0 && d.Standalone == nil
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

// WithWaiver returns the sidecar with w in it. A waiver for the same check and section is
// replaced, so one section holds one decision for one check.
func (d Decisions) WithWaiver(w Waiver) Decisions {
	out := d
	out.Waivers = slices.Clone(d.Waivers)
	for i, old := range out.Waivers {
		if old.Check == w.Check && slices.Equal(old.Section, w.Section) {
			out.Waivers[i] = w
			return out
		}
	}
	out.Waivers = append(out.Waivers, w)
	return out
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
