// Package profile loads, validates, and versions profiles: the configuration for one doc type
// (SDD §10.3, REQ-010 to REQ-013).
package profile

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/schemas"
)

// Profile is one doc type's configuration.
type Profile struct {
	Key        string     `yaml:"key" json:"key"`
	Name       string     `yaml:"name" json:"name"`
	Version    int        `yaml:"version,omitempty" json:"version,omitempty"`
	Template   string     `yaml:"template" json:"template"`
	Limits     Limits     `yaml:"limits" json:"limits"`
	Links      Links      `yaml:"links" json:"links"`
	Trace      Trace      `yaml:"trace" json:"trace"`
	Waivers    Waivers    `yaml:"waivers" json:"waivers"`
	Approvals  Approvals  `yaml:"approvals" json:"approvals"`
	Divergence Divergence `yaml:"divergence" json:"divergence"`
	Grounding  Grounding  `yaml:"grounding" json:"grounding"`
	Verify     Verify     `yaml:"verify" json:"verify"`
	Lint       Lint       `yaml:"lint" json:"lint"`
	Checks     []Check    `yaml:"checks" json:"checks"`
}

type Limits struct {
	MaxWords          int `yaml:"max_words" json:"max_words"`
	MaxSectionWords   int `yaml:"max_section_words" json:"max_section_words"`
	MaxSentenceWords  int `yaml:"max_sentence_words" json:"max_sentence_words"`
	MaxCodeBlockLines int `yaml:"max_code_block_lines" json:"max_code_block_lines"`
	MaxTableRows      int `yaml:"max_table_rows" json:"max_table_rows"`
}

type Links struct {
	Upstream *Upstream `yaml:"upstream,omitempty" json:"upstream,omitempty"`
	// Children is the link check that hardens with the doc's size: a doc at MinAt or larger
	// must link the bundles it covers (REQ-134).
	Children *Children `yaml:"children,omitempty" json:"children,omitempty"`
}

type Children struct {
	Kinds []string `yaml:"kinds" json:"kinds"`
	Min   int      `yaml:"min" json:"min"`
	MinAt string   `yaml:"min_at" json:"min_at"`
}

type Upstream struct {
	Kinds    []string `yaml:"kinds" json:"kinds"`
	Types    []string `yaml:"types" json:"types"`
	Required bool     `yaml:"required" json:"required"`
}

// Grounding holds the profile's grounding configuration. Sources is the source policy: which
// domains the grounding stage accepts, their tier, their freshness period, and the claim class
// of a section.
type Grounding struct {
	Sources sourcepolicy.Policy `yaml:"sources" json:"sources"`
}

// Verify configures the post-build verification gate: which trace IDs it verifies, and the
// bounds of its repo scan.
type Verify struct {
	// Prefixes are the trace ID prefixes the gate verifies. A decision ID or a goal ID names
	// no code, so reporting it missing makes the whole table noise.
	Prefixes []string `yaml:"prefixes" json:"prefixes"`
	// Exclude are path globs the scan passes over, such as the vendor directories.
	Exclude []string `yaml:"exclude" json:"exclude"`
	// MaxFileKB is the largest file the scan reads.
	MaxFileKB int `yaml:"max_file_kb" json:"max_file_kb"`
	// MaxMapperFiles bounds the files the AI mapper sees, highest ranked first.
	MaxMapperFiles int `yaml:"max_mapper_files" json:"max_mapper_files"`
}

type Trace struct {
	Prefixes []string `yaml:"prefixes" json:"prefixes"`
	Cover    []string `yaml:"cover" json:"cover"`
}

// Policy is a waiver policy value (SDD §9.1): a name, or n_approvals with a count.
type Policy struct {
	Name       string `json:"name,omitempty"`
	NApprovals int    `json:"n_approvals,omitempty"`
}

func (p *Policy) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		p.Name = n.Value
		return nil
	}
	var v struct {
		N int `yaml:"n_approvals"`
	}
	if err := n.Decode(&v); err != nil {
		return err
	}
	p.Name, p.NApprovals = "n_approvals", v.N
	return nil
}

type Waivers struct {
	Should Policy `yaml:"should" json:"should"`
	Must   Policy `yaml:"must" json:"must"`
}

type Approvals struct {
	Required int `yaml:"required" json:"required"`
}

type Divergence struct {
	Readers   int `yaml:"readers" json:"readers"`
	Questions struct {
		Min int `yaml:"min" json:"min"`
		Max int `yaml:"max" json:"max"`
	} `yaml:"questions" json:"questions"`
	Themes []string `yaml:"themes" json:"themes"`
}

type Lint struct {
	Overrides map[string]struct {
		Level string `yaml:"level" json:"level"`
	} `yaml:"overrides" json:"overrides"`
	SlopExtra []string `yaml:"slop_extra" json:"slop_extra"`
}

type Check struct {
	Slug     string  `yaml:"slug" json:"slug"`
	Level    string  `yaml:"level" json:"level"`
	Stage    string  `yaml:"stage" json:"stage"`
	Scope    string  `yaml:"scope" json:"scope"`
	Question string  `yaml:"question" json:"question"`
	PassWhen string  `yaml:"pass_when" json:"pass_when"`
	Waiver   *Policy `yaml:"waiver,omitempty" json:"waiver,omitempty"`
	// Sizes are the doc sizes this check applies to. An empty list applies at every size.
	Sizes []string `yaml:"sizes,omitempty" json:"sizes,omitempty"`
}

// AppliesAt reports whether the check runs on a doc of size sz.
func (c Check) AppliesAt(sz kernel.Size) bool {
	if len(c.Sizes) == 0 {
		return true
	}
	for _, s := range c.Sizes {
		if x, ok := kernel.ParseSize(s); ok && x == sz {
			return true
		}
	}
	return false
}

// Loaded is a profile with its source text and its template.
type Loaded struct {
	Profile Profile
	// Source is the profile YAML as written, and TemplateText the template it points to.
	// Together they decide the profile version (REQ-012).
	Source       []byte
	TemplateText []byte
	// Origin names where the profile came from: "built-in" or a file path.
	Origin string
}

// Defaults fill fields that a profile leaves out.
var defaults = Profile{
	Limits:    Limits{MaxWords: 8000, MaxSectionWords: 900, MaxSentenceWords: 25, MaxCodeBlockLines: 40, MaxTableRows: 15},
	Waivers:   Waivers{Should: Policy{Name: "non_author"}, Must: Policy{Name: "maintainer"}},
	Approvals: Approvals{Required: 1},
	Verify: Verify{
		Prefixes:       []string{"REQ", "NFR"},
		Exclude:        []string{"vendor/**", "node_modules/**", "dist/**", "build/**", ".git/**", "**/testdata/**", "**/*.min.js", "**/*.lock", "**/*.sum"},
		MaxFileKB:      512,
		MaxMapperFiles: 200,
	},
}

func init() {
	defaults.Divergence.Readers = 3
	defaults.Divergence.Questions.Min = 10
	defaults.Divergence.Questions.Max = 20
}

// ValidationError lists every schema error with its path (REQ-014).
type ValidationError struct {
	Origin string
	Errors []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s is not a valid profile:\n  %s", e.Origin, strings.Join(e.Errors, "\n  "))
}

var schema = func() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.Profile))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("profile.schema.json", doc); err != nil {
		panic(err)
	}
	return c.MustCompile("profile.schema.json")
}()

// Parse validates src against the profile schema and returns the profile. readTemplate reads
// the template path, relative to the profile file.
func Parse(origin string, src []byte, readTemplate func(string) ([]byte, error)) (Loaded, error) {
	var raw any
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return Loaded{}, &ValidationError{Origin: origin, Errors: []string{"the YAML does not parse: " + err.Error()}}
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return Loaded{}, &ValidationError{Origin: origin, Errors: []string{"the YAML has a value that JSON cannot hold: " + err.Error()}}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return Loaded{}, err
	}
	if err := schema.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return Loaded{}, &ValidationError{Origin: origin, Errors: flatten(ve)}
		}
		return Loaded{}, err
	}
	p := defaults
	p.Divergence.Themes = nil
	if err := yaml.Unmarshal(src, &p); err != nil {
		return Loaded{}, &ValidationError{Origin: origin, Errors: []string{err.Error()}}
	}
	var problems []string
	seen := map[string]bool{}
	for i, c := range p.Checks {
		if seen[c.Slug] {
			problems = append(problems, fmt.Sprintf("/checks/%d/slug: %q appears more than once", i, c.Slug))
		}
		seen[c.Slug] = true
	}
	if p.Divergence.Questions.Min > p.Divergence.Questions.Max {
		problems = append(problems, "/divergence/questions: min is larger than max")
	}
	if p.Verify.MaxFileKB <= 0 {
		p.Verify.MaxFileKB = defaults.Verify.MaxFileKB
	}
	if p.Verify.MaxMapperFiles <= 0 {
		p.Verify.MaxMapperFiles = defaults.Verify.MaxMapperFiles
	}
	// A children check with no min needs one link. The schema refuses 0, so 0 means left out.
	if p.Links.Children != nil && p.Links.Children.Min <= 0 {
		p.Links.Children.Min = 1
	}
	if err := p.Grounding.Sources.Validate(); err != nil {
		problems = append(problems, "/grounding/sources: "+err.Error())
	}
	tmpl, err := readTemplate(p.Template)
	if err != nil {
		problems = append(problems, fmt.Sprintf("/template: %q does not read: %v", p.Template, err))
	}
	if len(problems) > 0 {
		return Loaded{}, &ValidationError{Origin: origin, Errors: problems}
	}
	return Loaded{Profile: p, Source: src, TemplateText: tmpl, Origin: origin}, nil
}

var english = message.NewPrinter(language.English)

// flatten returns one line per leaf error: "<instance path>: <message>".
func flatten(ve *jsonschema.ValidationError) []string {
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			out = append(out, loc+": "+e.ErrorKind.LocalizedString(english))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	sort.Strings(out)
	return out
}

//go:embed builtin
var builtin embed.FS

// Builtins returns the profiles that ship in the binary (REQ-011).
func Builtins() ([]Loaded, error) {
	entries, err := fs.ReadDir(builtin, "builtin")
	if err != nil {
		return nil, err
	}
	var out []Loaded
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		src, err := fs.ReadFile(builtin, "builtin/"+e.Name())
		if err != nil {
			return nil, err
		}
		l, err := Parse("built-in "+e.Name(), src, func(p string) ([]byte, error) {
			return fs.ReadFile(builtin, path.Join("builtin", p))
		})
		if err != nil {
			return nil, err
		}
		l.Origin = "built-in"
		out = append(out, l)
	}
	return out, nil
}

// LoadLocal returns the built-in profiles with the files in dir (.speccy/profiles) laid over
// them: a file with the same key replaces the built-in profile (REQ-013). A missing dir is fine.
func LoadLocal(dir string) (map[string]Loaded, error) {
	builtins, err := Builtins()
	if err != nil {
		return nil, err
	}
	out := map[string]Loaded{}
	for _, l := range builtins {
		out[l.Profile.Key] = l
	}
	if dir == "" {
		return out, nil // hosted mode: built-ins only (REQ-013 profile editing comes later)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, f := range files {
		l, err := ParseFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[l.Profile.Key] = l
	}
	return out, errors.Join(errs...)
}

// ParseFile reads and validates the profile file f (REQ-014). Its template path is relative to
// the file and must stay in its folder.
func ParseFile(f string) (Loaded, error) {
	src, err := os.ReadFile(f)
	if err != nil {
		return Loaded{}, err
	}
	base := filepath.Dir(f)
	return Parse(f, src, func(p string) ([]byte, error) {
		if filepath.IsAbs(p) || strings.Contains(filepath.ToSlash(p), "..") {
			return nil, fmt.Errorf("the template path must stay inside %s", base)
		}
		return os.ReadFile(filepath.Join(base, filepath.FromSlash(p)))
	})
}

var requiredMark = regexp.MustCompile(`\s*<!--\s*required(?::\s*([a-z]+))?\s*-->\s*$`)

// RequiredHeadings returns the headings that the template marks as required, as heading paths.
// A heading line that ends with <!-- required --> is required at every size. A marker that
// names a size, such as <!-- required: app -->, is required at that size and larger (REQ-134).
func RequiredHeadings(template []byte) []Heading {
	var out []Heading
	body := StripMarks(template)
	marked := map[int]kernel.Size{}
	for i, line := range strings.Split(string(template), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := requiredMark.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		min := kernel.Feature
		if m[1] != "" {
			if sz, ok := kernel.ParseSize(m[1]); ok {
				min = sz
			}
		}
		marked[i+1] = min
	}
	lines := newLineIndex(body)
	for _, s := range section.Parse(body).Sections {
		if min, ok := marked[lines.line(s.Start)]; s.Level > 0 && ok {
			out = append(out, Heading{Level: s.Level, Title: s.Title, MinSize: min})
		}
	}
	return out
}

// Heading is a required heading: its level, its title, and the smallest doc size that must
// have it.
type Heading struct {
	Level   int
	Title   string
	MinSize kernel.Size
}

// StripMarks removes the required markers, so a new doc from the template reads clean
// (REQ-016).
func StripMarks(template []byte) []byte {
	lines := strings.Split(string(template), "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines[i] = requiredMark.ReplaceAllString(l, "")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

type lineIndex []int

func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, c := range src {
		if c == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

func (l lineIndex) line(off int) int {
	n := sort.Search(len(l), func(i int) bool { return l[i] > off })
	return n
}

// DocScope reports whether the check with slug reads the whole doc (scope: doc). Its finding
// is about the doc, so a waiver of it covers the whole doc, not the section the finding points at.
func (p Profile) DocScope(slug string) bool {
	for _, c := range p.Checks {
		if c.Slug == slug {
			return c.Scope != "section"
		}
	}
	return false
}
