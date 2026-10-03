package review

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
)

// PromptAsk turns a reviewer's concern into one question for the author
// (docs/specs/pr-review-batch.md).
const PromptAsk = "ask-v1"

var askSchema = []byte(`{"type":"object","additionalProperties":false,"required":["analysis","answer_quote","answered","question","quote","sections"],"properties":{` +
	`"analysis":{"type":"string"},"answer_quote":{"type":"string"},"answered":{"type":"boolean"},"question":{"type":"string"},` +
	`"quote":{"type":"string"},"sections":{"type":"array","items":{"type":"integer"}}}}`)

// AskDoc is one spec doc a concern can be about: its path in the repo and its text.
type AskDoc struct {
	Path string
	Text []byte
}

// AskPlace is one section of an AskDoc.
type AskPlace struct {
	Doc  int // the index of the doc
	Path []string
	sec  section.Section
}

// AskDraft is what the reviewer model made of a concern.
type AskDraft struct {
	// Question is the one question for the author.
	Question string
	// Places are the sections the concern is about, best first. Two mean the concern fits both
	// equally; none means no section fits.
	Places []AskPlace
	// Line is the line of the first place where the question belongs, from 1.
	Line int
	// Answered says the doc already answers the concern, and AnswerQuote is the text that does.
	Answered    bool
	AnswerQuote string
}

// Sections lists the sections of the docs that a concern can be about. The text before the
// first heading is a section with an empty heading path.
func Sections(docs []AskDoc) []AskPlace {
	var out []AskPlace
	for i, d := range docs {
		for _, sec := range section.Parse(d.Text).Sections {
			out = append(out, AskPlace{Doc: i, Path: sec.Path, sec: sec})
		}
	}
	return out
}

// DraftAsk asks the reviewer model which of places the concern is about, whether the docs
// already answer it, and for one question for the author.
func (s *Service) DraftAsk(ctx context.Context, docs []AskDoc, places []AskPlace, concern string) (AskDraft, error) {
	var out AskDraft
	if len(places) == 0 {
		return out, kernel.Invalid("no_section", "The spec docs of this pull request have no section.")
	}
	res, err := s.Gateway.Call(ctx, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptAsk, System: systemPrompt,
		Prompt: askPrompt(docs, places, concern), Schema: askSchema, MaxTokens: 3000,
	})
	if err != nil {
		return out, err
	}
	var a struct {
		AnswerQuote string `json:"answer_quote"`
		Answered    bool   `json:"answered"`
		Question    string `json:"question"`
		Quote       string `json:"quote"`
		Sections    []int  `json:"sections"`
	}
	if err := json.Unmarshal(res.JSON, &a); err != nil {
		return out, err
	}
	out.Question = strings.TrimSpace(a.Question)
	seen := map[int]bool{}
	for _, n := range a.Sections {
		if n >= 1 && n <= len(places) && !seen[n] {
			seen[n] = true
			out.Places = append(out.Places, places[n-1])
		}
	}
	if len(out.Places) > 0 {
		first := out.Places[0]
		out.Line = quoteLine(docs[first.Doc].Text, first.sec, a.Quote)
	}
	// An answer counts only with text that is in the docs, word for word.
	if q := strings.TrimSpace(a.AnswerQuote); a.Answered && q != "" {
		for _, d := range docs {
			if strings.Contains(section.Normalize(d.Text), section.Normalize([]byte(q))) {
				out.Answered, out.AnswerQuote = true, q
				break
			}
		}
	}
	return out, nil
}

// quoteLine is the line of quote in sec, or the line of the heading when the quote is not in
// the section. A quote can run over a line break of the file, so any run of white space in it
// matches any run in the text.
func quoteLine(src []byte, sec section.Section, quote string) int {
	at := sec.Start
	if words := strings.Fields(quote); len(words) > 0 {
		for i, w := range words {
			words[i] = regexp.QuoteMeta(w)
		}
		if loc := regexp.MustCompile(strings.Join(words, `\s+`)).FindIndex(src[sec.Start:sec.End]); loc != nil {
			at = sec.Start + loc[0]
		}
	}
	return strings.Count(string(src[:at]), "\n") + 1
}

func askPrompt(docs []AskDoc, places []AskPlace, concern string) string {
	var b strings.Builder
	b.WriteString("A reviewer reads the spec docs in the data parts below before a team builds from them. The reviewer has a concern, in the data part Concern. ")
	b.WriteString("Do three things.\n\n")
	b.WriteString("1. Find the sections the concern is about, from the numbered list of sections. Give the number of the best one. Give a second number only when two sections fit the concern equally well and the concern does not say which one it means. Give no number when no section fits.\n")
	b.WriteString("2. Decide if the docs already answer the concern: they state the fact that the concern asks for, in words a builder can act on. If they do, set answered to true, and copy the sentence that answers it word for word into answer_quote. Text that only mentions the subject does not answer it.\n")
	b.WriteString("3. Write one question for the author of the doc. The author must be able to answer it with one fact that a builder needs. Name the thing it is about, such as a component, a table, an endpoint or a job, and not the heading. Use plain words. Ask one thing. Do not suggest the answer, do not name review rules, and do not say what the reviewer thinks. Also copy one line of the best section, word for word, into quote: the line the question is about, or an empty string when the question is about something the section leaves out.\n\n")
	b.WriteString("Write your reasoning first, in analysis.\n\nSections:\n")
	for i, p := range places {
		heading := strings.Join(p.Path, " › ")
		if heading == "" {
			heading = "(the text before the first heading)"
		}
		fmt.Fprintf(&b, "%d: %s, %s\n", i+1, docs[p.Doc].Path, heading)
	}
	b.WriteString("\n")
	for _, d := range docs {
		b.WriteString(data("Spec doc "+d.Path, string(d.Text)))
		b.WriteString("\n")
	}
	b.WriteString(data("Concern", concern))
	return b.String()
}
