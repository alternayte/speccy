package source

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// RepoConfigFile is the repo configuration file at the root of the served folder (SDD §10.4).
const RepoConfigFile = ".speccy.yaml"

// RepoConfig is .speccy.yaml.
type RepoConfig struct {
	// Bundles limits folder bundles (REQ-001 form a) to folders that match a glob. With no
	// entry, every folder with a main doc is a bundle.
	Bundles []struct {
		Path string `yaml:"path"`
	} `yaml:"bundles"`
	// Map makes single-file bundles (REQ-130): a markdown file that matches a glob is a bundle
	// with that profile. A frontmatter type wins over the mapping.
	Map []Mapping `yaml:"map"`
	// Frontmatter maps a key of Speccy to the key a team's template uses for it (#93). Only
	// size has a mapping: "size: sdd_level" reads the size of a doc from its sdd_level key.
	Frontmatter struct {
		Keys map[string]string `yaml:"keys"`
	} `yaml:"frontmatter"`
	// LinkRules are link rules by path convention (REQ-132): "<from> <kind> <to>".
	LinkRules []string `yaml:"link_rules"`
	// LinkPatterns expand a short external target key to a URL, by scheme. A pattern holds
	// {key}, such as "https://company.atlassian.net/browse/{key}" for jira.
	LinkPatterns map[string]string `yaml:"link_patterns"`
	Adoption     struct {
		// Relaxed lists check slugs that report at level INFO (REQ-133).
		Relaxed []string `yaml:"relaxed"`
	} `yaml:"adoption"`
	Mode        string   `yaml:"mode"`
	Server      string   `yaml:"server"`
	Enforcement string   `yaml:"enforcement"`
	PR          PRConfig `yaml:"pr"`
}

// PRConfig is the pr section of .speccy.yaml: how Speccy comments on a pull request.
type PRConfig struct {
	InlineLimit int `yaml:"inline_limit"`
	// Levels are the finding levels that go inline as comments: must, should, or both. With
	// none, MUST findings go inline, and a SHOULD finding only with a suggestion.
	Levels []string `yaml:"levels"`
	// Attribution none keeps the name of Speccy out of a pending review: no heading, no
	// catalog link and no hidden marker. The default is speccy.
	Attribution string `yaml:"attribution"`
}

// Finding levels that pr.levels takes, and the values of pr.attribution.
const (
	LevelMust         = "must"
	LevelShould       = "should"
	AttributionSpeccy = "speccy"
	AttributionNone   = "none"
)

// ParseLevels reads a list of levels, such as "must,should".
func ParseLevels(s string) ([]string, error) {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			out = append(out, l)
		}
	}
	if p := levelsProblem(out); p != "" {
		return nil, errors.New(p)
	}
	return out, nil
}

func levelsProblem(levels []string) string {
	if levels != nil && len(levels) == 0 {
		return "names no level. Name must, should, or both"
	}
	for _, l := range levels {
		if l != LevelMust && l != LevelShould {
			return fmt.Sprintf("%q is not must or should", l)
		}
	}
	return ""
}

// Mapping maps a glob of markdown files to a profile key, and optionally to a size, for a doc
// that names no size itself (#93).
type Mapping struct {
	Glob    string `yaml:"glob"`
	Profile string `yaml:"profile"`
	Size    string `yaml:"size"`
}

// LoadRepoConfig reads root/.speccy.yaml. A missing file is an empty config.
func LoadRepoConfig(root string) (RepoConfig, error) {
	var c RepoConfig
	src, err := os.ReadFile(filepath.Join(root, RepoConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return ParseRepoConfig(src)
}

// FindRepoConfig reads root/.speccy.yaml, and reports whether the file exists.
func FindRepoConfig(root string) (cfg RepoConfig, found bool, err error) {
	src, err := os.ReadFile(filepath.Join(root, RepoConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, true, err
	}
	cfg, err = ParseRepoConfig(src)
	return cfg, true, err
}

// ParseRepoConfig parses .speccy.yaml. An unknown key is an error, so a typo does not pass
// silently.
func ParseRepoConfig(src []byte) (RepoConfig, error) {
	var c RepoConfig
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s does not parse: %w", RepoConfigFile, err)
	}
	var problems []string
	for i, b := range c.Bundles {
		if !doublestar.ValidatePattern(b.Path) {
			problems = append(problems, fmt.Sprintf("bundles[%d].path %q is not a valid glob", i, b.Path))
		}
	}
	for i, m := range c.Map {
		if !doublestar.ValidatePattern(m.Glob) || m.Glob == "" {
			problems = append(problems, fmt.Sprintf("map[%d].glob %q is not a valid glob", i, m.Glob))
		}
		if m.Profile == "" {
			problems = append(problems, fmt.Sprintf("map[%d] has no profile", i))
		}
		if _, ok := kernel.ParseSize(m.Size); m.Size != "" && !ok {
			problems = append(problems, fmt.Sprintf("map[%d].size %q is not feature, app or initiative", i, m.Size))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(c.Frontmatter.Keys)) {
		if key != "size" {
			problems = append(problems, fmt.Sprintf("frontmatter.keys has %q, and only size has a mapping", key))
		} else if strings.TrimSpace(c.Frontmatter.Keys[key]) == "" {
			problems = append(problems, "frontmatter.keys.size names no key")
		}
	}
	for i, r := range c.LinkRules {
		if _, err := ParseLinkRule(r); err != nil {
			problems = append(problems, fmt.Sprintf("link_rules[%d]: %v", i, err))
		}
	}
	for _, scheme := range slices.Sorted(maps.Keys(c.LinkPatterns)) {
		if p := LinkPatternProblem(scheme, c.LinkPatterns[scheme]); p != "" {
			problems = append(problems, "link_patterns: "+p)
		}
	}
	switch c.Mode {
	case "", "standalone", "connected":
	default:
		problems = append(problems, fmt.Sprintf("mode %q is not standalone or connected", c.Mode))
	}
	switch c.Enforcement {
	case "", "advisory", "blocking":
	default:
		problems = append(problems, fmt.Sprintf("enforcement %q is not advisory or blocking", c.Enforcement))
	}
	if c.PR.Levels != nil {
		if p := levelsProblem(c.PR.Levels); p != "" {
			problems = append(problems, "pr.levels "+p)
		}
	}
	switch c.PR.Attribution {
	case "", AttributionSpeccy, AttributionNone:
	default:
		problems = append(problems, fmt.Sprintf("pr.attribution %q is not speccy or none", c.PR.Attribution))
	}
	if len(problems) > 0 {
		return c, fmt.Errorf("%s: %s", RepoConfigFile, strings.Join(problems, "; "))
	}
	return c, nil
}

// MappedProfile returns the profile that a mapping gives the file at rel, if any.
func (c RepoConfig) MappedProfile(rel string) (string, bool) {
	m, ok := c.MapEntry(rel)
	return m.Profile, ok
}

// MapEntry returns the first map entry that covers the file at rel, if any. Only that entry
// applies to the file.
func (c RepoConfig) MapEntry(rel string) (Mapping, bool) {
	for _, m := range c.Map {
		if ok, _ := doublestar.Match(m.Glob, rel); ok {
			return m, true
		}
	}
	return Mapping{}, false
}

// NamedSize is one place that names the size of a doc, and the value it names there.
type NamedSize struct {
	Value string
	// Where says the place in words, for a run note.
	Where string
}

// NamedSizes returns each place that names the size of the doc at rel, in the order Speccy
// reads them: the size key of the frontmatter, the key of the team's template that
// frontmatter.keys maps to size, and the size of the map entry that covers the doc. A place
// that names nothing is left out.
func (c RepoConfig) NamedSizes(rel string, content []byte) []NamedSize {
	var out []NamedSize
	add := func(value, where string) {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, NamedSize{Value: value, Where: where})
		}
	}
	fm, _, _ := ReadFrontmatter(content)
	add(fm.Size, "the frontmatter key size")
	if key := strings.TrimSpace(c.Frontmatter.Keys["size"]); key != "" && key != "size" {
		add(FrontmatterValue(content, key), "the frontmatter key "+key)
	}
	if m, ok := c.MapEntry(rel); ok {
		add(m.Size, "the map entry for "+m.Glob+" in "+RepoConfigFile)
	}
	return out
}

// BundleFolderAllowed reports whether dir may hold a folder bundle.
func (c RepoConfig) BundleFolderAllowed(dir string) bool {
	if len(c.Bundles) == 0 {
		return true
	}
	for _, b := range c.Bundles {
		if ok, _ := doublestar.Match(b.Path, dir); ok {
			return true
		}
	}
	return false
}

// Relaxed reports whether a check is in adoption mode.
func (c RepoConfig) Relaxed(slug string) bool {
	for _, s := range c.Adoption.Relaxed {
		if s == slug {
			return true
		}
	}
	return false
}

// LinkKinds are the link kinds of REQ-050. ExternalKind names a code target, so only a
// frontmatter link uses it.
var LinkKinds = []string{"implements", "refines", "references", "supersedes", ExternalKind}

// BundleLinkKinds are the kinds a link rule may use: a rule links two bundles.
var BundleLinkKinds = LinkKinds[:len(LinkKinds)-1]

// LinkRule links spec docs by path convention (REQ-132): "docs/sdd-{name}.md implements
// docs/prd-{name}.md". A path is a spec doc's file, relative to the root. {name} matches one
// path segment or part of one. {name*} matches one or more whole segments, so one rule covers
// docs at every depth.
type LinkRule struct {
	From string
	Kind string
	To   string
	re   *regexp.Regexp
	vars []string
}

// ruleVar finds a variable. The name it captures holds the star of a {name*}.
var ruleVar = regexp.MustCompile(`\{([a-z_]+\*?)\}`)

// ParseLinkRule parses one rule.
func ParseLinkRule(rule string) (LinkRule, error) {
	parts := strings.Fields(rule)
	if len(parts) != 3 {
		return LinkRule{}, fmt.Errorf("%q is not \"<from> <kind> <to>\"", rule)
	}
	r := LinkRule{From: parts[0], Kind: parts[1], To: parts[2]}
	if !slices.Contains(BundleLinkKinds, r.Kind) {
		return LinkRule{}, fmt.Errorf("%q: the kind %q is not one of %s", rule, r.Kind, strings.Join(BundleLinkKinds, ", "))
	}
	// Brace text that is not a variable would be a literal that no path has, so the rule would
	// link nothing and say nothing.
	for _, p := range []string{r.From, r.To} {
		if rest := ruleVar.ReplaceAllString(p, ""); strings.ContainsAny(rest, "{}") {
			return LinkRule{}, fmt.Errorf("%q: %q has a brace that is not a variable. A variable is {name} or {name*}, with lower-case letters and _", rule, p)
		}
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	last := 0
	for _, m := range ruleVar.FindAllStringSubmatchIndex(r.From, -1) {
		pattern.WriteString(regexp.QuoteMeta(r.From[last:m[0]]))
		name := r.From[m[2]:m[3]]
		if slices.Contains(r.vars, name) {
			return LinkRule{}, fmt.Errorf("%q: {%s} appears twice in the first path", rule, name)
		}
		r.vars = append(r.vars, name)
		if strings.HasSuffix(name, "*") {
			if (m[0] > 0 && r.From[m[0]-1] != '/') || (m[1] < len(r.From) && r.From[m[1]] != '/') {
				return LinkRule{}, fmt.Errorf("%q: {%s} stands for whole folders, so it must be between two / or at an end of the path", rule, name)
			}
			pattern.WriteString("(.+)")
		} else {
			pattern.WriteString("([^/]+)")
		}
		last = m[1]
	}
	pattern.WriteString(regexp.QuoteMeta(r.From[last:]) + "$")
	r.re = regexp.MustCompile(pattern.String())
	for _, m := range ruleVar.FindAllStringSubmatch(r.To, -1) {
		if !slices.Contains(r.vars, m[1]) {
			return LinkRule{}, fmt.Errorf("%q: {%s} is in the second path but not the first", rule, m[1])
		}
	}
	return r, nil
}

// Target returns the path that the rule links from, when from matches the rule.
func (r LinkRule) Target(from string) (string, bool) {
	m := r.re.FindStringSubmatch(from)
	if m == nil {
		return "", false
	}
	to := r.To
	for i, name := range r.vars {
		to = strings.ReplaceAll(to, "{"+name+"}", m[i+1])
	}
	return to, true
}

// Enforce removes a check slug from the adoption mode list of .speccy.yaml, so the check
// reports at its own level again (REQ-133). ok is false when the slug is not in the list. The
// rest of the file keeps its keys and its comments.
func Enforce(src []byte, slug string) (out []byte, ok bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, false, fmt.Errorf("%s does not parse: %w", RepoConfigFile, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, nil
	}
	list := findKey(findKey(doc.Content[0], "adoption"), "relaxed")
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil, false, nil
	}
	for i, n := range list.Content {
		if n.Value != slug {
			continue
		}
		list.Content = append(list.Content[:i], list.Content[i+1:]...)
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return nil, false, err
		}
		if err := enc.Close(); err != nil {
			return nil, false, err
		}
		return buf.Bytes(), true, nil
	}
	return nil, false, nil
}

// Relax adds check slugs to the adoption mode list of .speccy.yaml, so they report at level
// INFO (REQ-133). A slug in the list already is left as it is. The rest of the file keeps its
// keys and its comments.
func Relax(src []byte, slugs []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", RepoConfigFile, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a mapping", RepoConfigFile)
	}
	root := doc.Content[0]
	adoption := findKey(root, "adoption")
	if adoption == nil {
		adoption = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "adoption"}, adoption)
	}
	if adoption.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("adoption in %s is not a mapping", RepoConfigFile)
	}
	list := findKey(adoption, "relaxed")
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		adoption.Content = append(adoption.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "relaxed"}, list)
	}
	if list.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("adoption.relaxed in %s is not a list", RepoConfigFile)
	}
	for _, s := range slugs {
		if slices.ContainsFunc(list.Content, func(n *yaml.Node) bool { return n.Value == s }) {
			continue
		}
		list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s})
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// findKey returns the value node of key in a mapping, or nil.
func findKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// AddMappings adds mappings to the repo's .speccy.yaml and returns the new file. was is the
// file's current bytes, or nil when the repo has none. It edits the YAML in place, so the
// comments and the other keys of the file stay as they are.
func AddMappings(was []byte, add []Mapping) ([]byte, error) {
	if len(add) == 0 {
		return was, nil
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(was)) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	} else if err := yaml.Unmarshal(was, &doc); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", RepoConfigFile, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a mapping", RepoConfigFile)
	}
	root := doc.Content[0]
	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "map" {
			list = root.Content[i+1]
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "map"}, list)
	}
	if list.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("map in %s is not a list", RepoConfigFile)
	}
	for _, m := range add {
		entry := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "glob"}, {Kind: yaml.ScalarNode, Value: m.Glob},
			{Kind: yaml.ScalarNode, Value: "profile"}, {Kind: yaml.ScalarNode, Value: m.Profile},
		}}
		list.Content = append(list.Content, entry)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
