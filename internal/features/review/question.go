package review

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/http/api"
)

// quoted finds the first text in double quotes in a message.
var quoted = regexp.MustCompile(`"([^"]+)"`)

// answerQuestion is the question that the author must answer to fix an answer finding (#110).
// A finding of a rubric check has the question that the reviewer wrote for its shortfall, in
// written. Every other finding gets its question from what Speccy knows about it: the build
// question, the claim, the quoted text. A person, or an agent that asks the person, then needs
// no guess about which fact is missing. A reword finding needs no fact, and has no question.
func answerQuestion(slug, message, quote, written string, evidence []byte) string {
	if fixKind(slug, evidence) != api.Answer {
		return ""
	}
	if written = strings.TrimSpace(written); written != "" {
		return written
	}
	quote = strings.Join(strings.Fields(quote), " ")
	var ev struct {
		Question   string `json:"question"`
		Claim      string `json:"claim"`
		ID         string `json:"id"`
		Upstream   string `json:"upstream"`
		File       string `json:"file"`
		Downstream string `json:"downstream"`
	}
	_ = json.Unmarshal(evidence, &ev)
	named := ""
	if m := quoted.FindStringSubmatch(message); m != nil {
		named = m[1]
	}
	switch slug {
	case lint.Placeholder:
		return fmt.Sprintf("What is the real content in place of %q?", quote)
	case lint.RequiredHeadings:
		return fmt.Sprintf("What does this doc say under the heading %q?", named)
	case lint.Weasel:
		return fmt.Sprintf("Which number, name or condition replaces %q?", quote)
	case lint.UndefinedAcronym:
		return fmt.Sprintf("What does %s stand for?", quote)
	case lint.DanglingRef:
		return fmt.Sprintf("Which item does %s name? Give its definition, or the ID that the reference must have.", quote)
	case lint.DuplicateID:
		return fmt.Sprintf("%s has two definitions. Which item keeps the ID, and which ID does the other item get?", quote)
	case lint.BrokenLink:
		return fmt.Sprintf("Which file does the link %q point to?", quote)
	case lint.ProseLimit:
		return "Which detail of this text can move to an asset or to a linked doc?"
	case lint.AssetNudge:
		return "Which file in assets/ takes this block?"
	case lint.RequirementGrammar:
		return fmt.Sprintf("What triggers %s, and what is the response?", strings.Fields(message + " ")[0])
	case HasUpstreamSlug:
		return "Which upstream doc does this doc build on? Or why does it stand alone?"
	case HasChildrenSlug:
		return "Which bundles does this doc cover?"
	case ExternalTargetSlug:
		return "What is the correct target of this link?"
	case CoverageSlug:
		return fmt.Sprintf("Where does this doc cover %s of %s? Or does another doc cover it, or is it out of scope?", ev.ID, ev.Upstream)
	case DivergenceAmbiguous, DivergenceGap:
		if ev.Question != "" {
			return ev.Question
		}
	case GroundingUnverified:
		if ev.Claim != "" {
			return fmt.Sprintf("Which source confirms this claim, or is it an assumption: %q?", ev.Claim)
		}
	case GroundingContradicted:
		if ev.Claim != "" {
			return fmt.Sprintf("A source says otherwise. What is the correct fact for this claim: %q?", ev.Claim)
		}
	case GroundingFileContradicts:
		if ev.Claim != "" && ev.File != "" {
			return fmt.Sprintf("%s says otherwise. Which is right, the file or this claim: %q?", ev.File, ev.Claim)
		}
	case ContradictionSlug, ExternalConflictSlug:
		return "Which statement is right: the one in this doc, or the one in the linked doc?"
	case DownstreamRequestSlug:
		return fmt.Sprintf("Which statement is right: the one in this doc, or the one in %s?", ev.Downstream)
	case RestatementSlug:
		return fmt.Sprintf("What does this paragraph add to %s?", ev.Upstream)
	}
	return "Which fact fixes this? " + message
}
