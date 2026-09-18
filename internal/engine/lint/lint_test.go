package lint

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/speccy/internal/kernel"
)

var base = Config{
	Path: "SPEC.md", Files: []string{"SPEC.md", "assets/api.yaml", "assets/flow.png"},
	MaxWords: 8000, MaxSectionWords: 900, MaxSentenceWords: 25, MaxCodeBlockLines: 40, MaxTableRows: 15,
	Prefixes: []string{"REQ", "DEC", "NFR"}, UpstreamPrefixes: []string{"REQ", "NFR"},
}

func findings(src string, cfg Config, slug string) []Finding {
	var out []Finding
	for _, f := range Run([]byte(src), cfg).Findings {
		if f.Slug == slug {
			out = append(out, f)
		}
	}
	return out
}

func quotes(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Anchor.Quote
	}
	return out
}

func TestRules(t *testing.T) {
	cases := []struct {
		name string
		slug string
		src  string
		cfg  func(*Config)
		want []string // quotes of the findings, in order
	}{
		{"placeholders", Placeholder,
			"---\ntype: sdd\n---\n# T\n\nThe limit is TBD. {{name}} owns it. See <Product name> and <b>bold</b>.\n\n`TODO` in code is fine. A todo list is fine.\n",
			nil, []string{"TBD", "{{name}}", "<Product name>"}},
		{"placeholder with commas", Placeholder, "# T\n\n<Who has the problem, and why.>\n", nil,
			[]string{"<Who has the problem, and why.>"}},
		{"required headings", RequiredHeadings, "# T\n\n## Goals\n\nx\n",
			func(c *Config) { c.Required = []Heading{{2, "Goals"}, {2, "Non-goals"}} }, []string{"# T"}},
		{"broken links", BrokenLink,
			"# T\n\n[ok](assets/api.yaml) ![ok](assets/flow.png) [web](https://x.dev) [anchor](#t) [gone](assets/gone.yaml) [out](../x.md) [dir](assets)\n",
			nil, []string{"gone", "out"}},
		{"duplicate IDs", DuplicateID,
			"# T\n\n- **DEC-001:** One.\n- **DEC-002:** Two.\n- **DEC-001:** Again.\n\n### DEC-002: Heading form\n",
			nil, []string{"DEC-001", "DEC-002"}},
		{"dangling references", DanglingRef,
			"# T\n\n- **DEC-001:** One, see DEC-002 and DEC-001. It implements REQ-009.\n", nil, []string{"DEC-002"}},
		{"slop", SlopPhrase, "# T\n\nIt's worth noting that we leverage a robust design. Synergy.\n",
			func(c *Config) { c.SlopExtra = []string{"synergy"} }, []string{"It's worth noting", "leverage", "robust", "Synergy"}},
		{"weasel", Weasel, "# T\n\nSome users retry various times, etc. The limit is 3.\n", nil,
			[]string{"Some", "various", "etc."}},
		{"sentence length", SentenceLength,
			"# T\n\nShort one. Word " + strings.Repeat("word ", 30) + "end. Another short one, e.g. this one.\n", nil,
			[]string{"Word " + strings.Repeat("word ", 30) + "end."}},
		{"acronyms", UndefinedAcronym,
			"# T\n\nThe PSP sends a webhook. The API uses Payment Service Provider (PSP) terms. The RTO is low. The HTTP call uses REQ-001.\n",
			nil, []string{"PSP", "RTO"}},
		{"acronym defined first", UndefinedAcronym, "# T\n\nThe Recovery Time Objective (RTO) is 1 h. The RTO holds.\n", nil, nil},
		{"rfc2119 case", RFC2119Case,
			"# T\n\n- **REQ-001:** The service must retry. It MUST log.\n- **DEC-001:** We may cache.\n- Users should see it.\n", nil,
			[]string{"must"}},
		{"asset nudge", AssetNudge, "# T\n\n```\n" + strings.Repeat("x\n", 41) + "```\n\n| a |\n|---|\n" + strings.Repeat("| 1 |\n", 16), nil,
			[]string{"x\n", "a"}},
		{"passive voice", PassiveVoice, "# T\n\nThe request is retried by the client. The server retries.\n", nil,
			[]string{"is retried"}},
		{"rule off", Weasel, "# T\n\nSome users.\n", func(c *Config) { c.Levels = map[string]string{Weasel: "off"} }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			if c.cfg != nil {
				c.cfg(&cfg)
			}
			got := quotes(findings(c.src, cfg, c.slug))
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("findings = %q, want %q", got, c.want)
			}
		})
	}
}

func TestProseLimits(t *testing.T) {
	cfg := base
	cfg.MaxWords, cfg.MaxSectionWords = 20, 8
	src := "# T\n\n## A\n\n" + strings.Repeat("word ", 9) + "\n\n## B\n\n" + strings.Repeat("word ", 7) + "\n\n### C\n\n" + strings.Repeat("word ", 7) + "\n"
	got := findings(src, cfg, ProseLimit)
	var msgs []string
	for _, f := range got {
		msgs = append(msgs, f.Message)
	}
	want := []string{"The doc has 23 words. The limit is 20.", "This section has 9 words. The limit is 8."}
	if fmt.Sprint(msgs) != fmt.Sprint(want) {
		t.Errorf("messages = %q, want %q", msgs, want)
	}
}

func TestAnchors(t *testing.T) {
	src := "---\ntype: sdd\n---\n# Pay\n\n## Retries\n\nThe limit is TBD for now.\n"
	f := findings(src, base, Placeholder)
	if len(f) != 1 {
		t.Fatalf("findings = %+v", f)
	}
	a := f[0].Anchor
	if src[a.Start:a.End] != "TBD" || a.Quote != "TBD" || a.Prefix != src[a.Start-len(a.Prefix):a.Start] ||
		fmt.Sprint(a.HeadingPath) != "[Pay Retries]" || a.File != "SPEC.md" {
		t.Errorf("anchor = %+v", a)
	}
	if f[0].Level != kernel.Must {
		t.Errorf("level = %s", f[0].Level)
	}
}

// tenK builds a doc of about 10,000 words with every kind of block.
func tenK() []byte {
	var b strings.Builder
	b.WriteString("---\ntype: sdd\ntitle: Load\n---\n# Load test\n\n")
	for i := 0; b.Len() < 70000; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n", i)
		fmt.Fprintf(&b, "- **REQ-%03d:** The service MUST retry a failed call up to 3 times, and it logs each attempt. See DEC-%03d.\n", i, i)
		fmt.Fprintf(&b, "- **DEC-%03d:** The Payment Service Provider (PSP) owns retries; the client does not.\n\n", i)
		b.WriteString("The payment service calls the provider and records each attempt. Some requests are retried by the queue. ")
		b.WriteString("It is worth noting that the limit is 10 s per attempt, and the backoff doubles each time up to 800 ms.\n\n")
		b.WriteString("| Limit | Value |\n|---|---|\n| Attempts | 4 |\n| Timeout | 10 s |\n\n")
		b.WriteString("```go\nfunc retry() {}\n```\n\n")
	}
	return []byte(b.String())
}

// T-081: lint finishes a 10,000-word doc in under 1 second. `just test` runs this benchmark,
// and it fails when one run takes longer.
func BenchmarkLint_10kWords(b *testing.B) {
	src := tenK()
	if n := len(strings.Fields(string(src))); n < 10000 {
		b.Fatalf("the doc has %d words, want at least 10,000", n)
	}
	b.ResetTimer()
	start := time.Now()
	for range b.N {
		Run(src, base)
	}
	if per := time.Since(start) / time.Duration(b.N); per > time.Second {
		b.Fatalf("lint took %s for 10,000 words; the limit is 1s (REQ-061)", per)
	}
}

// T-006 in part: lint is deterministic.
func TestRun_Deterministic(t *testing.T) {
	src := tenK()
	a, b := Run(src, base), Run(src, base)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Error("two runs on the same doc differ")
	}
}
