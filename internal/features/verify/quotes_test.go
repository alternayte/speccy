package verify

import (
	"testing"

	"github.com/alternayte/speccy/internal/engine/verify"
)

// A judge that leaves text out with an ellipsis keeps its verdict when every piece is in one
// cited file. A piece that is in no file, or pieces spread over two files, lose it.
func TestCheckQuotesAcceptsAnEllipsisOnlyWithinOneFile(t *testing.T) {
	req := requirement{ID: "REQ-002", Text: "The list MUST show only the pairs that match, ignoring letter case."}
	a := &API{}
	code := []citedCode{
		{Path: "a.ts", Body: "const q = s.toLowerCase();\nconst x = 1;\nreturn pairs.filter((p) => p.includes(q));\n"},
		{Path: "b.ts", Body: "export const other = 2;\n"},
	}
	judged := func(quote string) verify.Judgement {
		return a.checkQuotes(Judgement{Verdict: verify.Affirmed, RequirementQuote: "show only the pairs ... ignoring letter case",
			CodeQuote: quote}, req, code).Verdict
	}
	if v := judged("const q = s.toLowerCase();\n...\nreturn pairs.filter((p) => p.includes(q));"); v != verify.Affirmed {
		t.Errorf("pieces from one file gave %s", v)
	}
	if v := judged("const q = s.toLowerCase(); … return pairs.sort();"); v != verify.Silent {
		t.Errorf("an invented piece gave %s", v)
	}
	if v := judged("const q = s.toLowerCase(); ... export const other = 2;"); v != verify.Silent {
		t.Errorf("pieces from two files gave %s", v)
	}
}

// A mapper quote that misses snaps to the one file line it names, copied from the file.
func TestSnapLine(t *testing.T) {
	body := "function f() {\n        return matches.slice(0, LIMIT);\n}\n"
	got, ok := snapLine(body, "  const x = 1;\nreturn matches.slice(0, LIMIT);")
	if !ok || got != "        return matches.slice(0, LIMIT);" {
		t.Errorf("snapLine = %q, %v", got, ok)
	}
	if _, ok := snapLine(body+"return matches.slice(0, LIMIT);\n", "return matches.slice(0, LIMIT);"); ok {
		t.Error("a line that appears twice names no one place")
	}
	if _, ok := snapLine(body, "}"); ok {
		t.Error("a brace names no one place")
	}
}
