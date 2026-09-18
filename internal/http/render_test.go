package http

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// DEC-017: each block carries its source position; the preview and overlays depend on it.
func TestRender_SourcePositions(t *testing.T) {
	src := "---\ntype: sdd\n---\n# Title\n\nPara one\ncontinues.\n\n```go\nx := 1\n```\n\n![d](img/a.png)\n"
	out, err := Render([]byte(src), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for tag, line := range map[string]string{"h1": "4", "p": "6", "pre class=\"code": "9"} {
		re := regexp.MustCompile(`<` + tag + ` [^>]*data-line="(\d+)"`)
		if m := re.FindStringSubmatch(out); m == nil || m[1] != line {
			t.Errorf("<%s> data-line = %v, want %s\n%s", tag, m, line, out)
		}
	}
	code := regexp.MustCompile(`<pre class="code chroma" data-src-start="(\d+)" data-src-end="(\d+)"`).FindStringSubmatch(out)
	if code == nil || src[atoi(code[1]):atoi(code[2])] != "```go\nx := 1\n```\n" {
		t.Errorf("code block span %v does not cover the fences", code)
	}

	id := uuid.MustParse("01a0b65d-8f0a-7b1b-b8bc-b89126821393")
	out, err = Render([]byte("![d](../img/a.png)\n![e](img/b.png)\n<script>alert(1)</script>\n"), &id, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `src="/api/v1/bundles/`+id.String()+`/files/content?path=img%2Fa.png"`) ||
		!strings.Contains(out, "path=docs%2Fimg%2Fb.png") {
		t.Errorf("image links are not resolved in the bundle:\n%s", out)
	}
	if strings.Contains(out, "<script") {
		t.Error("raw HTML was rendered")
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
