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
	// LinkRules are link rules by path convention (REQ-132): "<from> <kind> <to>".
	LinkRules []string `yaml:"link_rules"`
	// LinkPatterns expand a short external target key to a URL, by scheme. A pattern holds
	// {key}, such as "https://company.atlassian.net/browse/{key}" for jira.
	LinkPatterns map[string]string `yaml:"link_patterns"`
	Adoption     struct {
		// Relaxed lists check slugs that report at level INFO (REQ-133).
		Relaxed []string `yaml:"relaxed"`
	} `yaml:"adoption"`
	Mode        string `yaml:"mode"`
	Server      string `yaml:"server"`
	Enforcement string `yaml:"enforcement"`
	PR          struct {
		InlineLimit int `yaml:"inline_limit"`
	} `yaml:"pr"`
}

// Mapping maps a glob of markdown files to a profile key.
type Mapping struct {
	Glob    string `yaml:"glob"`
	Profile string `yaml:"profile"`
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
	if len(problems) > 0 {
		return c, fmt.Errorf("%s: %s", RepoConfigFile, strings.Join(problems, "; "))
	}
	return c, nil
}

// MappedProfile returns the profile that a mapping gives the file at rel, if any.
func (c RepoConfig) MappedProfile(rel string) (string, bool) {
	for _, m := range c.Map {
		if ok, _ := doublestar.Match(m.Glob, rel); ok {
			return m.Profile, true
		}
	}
	return "", false
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

// LinkRule links bundles by path convention (REQ-132): "docs/sdd-{name}.md implements
// docs/prd-{name}.md". A path is a single-file bundle's file, or a folder bundle's folder,
// relative to the root. {name} matches one path segment or part of one.
type LinkRule struct {
	From string
	Kind string
	To   string
	re   *regexp.Regexp
	vars []string
}

var ruleVar = regexp.MustCompile(`\{([a-z_]+)\}`)

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
		pattern.WriteString("([^/]+)")
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
