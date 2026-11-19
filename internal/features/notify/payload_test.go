package notify

import (
	"encoding/json"
	"testing"
)

// Slack takes text with a <url|title> link and escapes &, < and >. A Teams Workflows webhook
// takes a message with one Adaptive Card attachment; a plain text body fails there.
func TestPayloadFormats(t *testing.T) {
	m := message{Text: "Kim asks for a review. For Lee & Sam.", Title: "Pay <v2>", Link: "https://speccy.example.com/bundles/1"}
	var slack struct {
		Text string `json:"text"`
	}
	raw0, _ := json.Marshal(payload(Slack, m))
	_ = json.Unmarshal(raw0, &slack)
	if want := "<https://speccy.example.com/bundles/1|Pay &lt;v2&gt;>: Kim asks for a review. For Lee &amp; Sam."; slack.Text != want {
		t.Errorf("slack text %q, want %q", slack.Text, want)
	}
	var teams struct {
		Type        string `json:"type"`
		Attachments []struct {
			ContentType string `json:"contentType"`
			Content     struct {
				Type    string `json:"type"`
				Actions []struct {
					URL string `json:"url"`
				} `json:"actions"`
			} `json:"content"`
		} `json:"attachments"`
	}
	raw, _ := json.Marshal(payload(Teams, m))
	if err := json.Unmarshal(raw, &teams); err != nil {
		t.Fatal(err)
	}
	if teams.Type != "message" || len(teams.Attachments) != 1 || teams.Attachments[0].ContentType != "application/vnd.microsoft.card.adaptive" ||
		teams.Attachments[0].Content.Type != "AdaptiveCard" || teams.Attachments[0].Content.Actions[0].URL != m.Link {
		t.Errorf("teams payload %s", raw)
	}
}
