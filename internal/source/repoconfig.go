package source

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	// LinkRules are link rules by path convention (REQ-132). M7 reads them.
	LinkRules []string `yaml:"link_rules"`
	Adoption  struct {
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
