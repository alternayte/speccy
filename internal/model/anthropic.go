package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicBackend calls the Claude API through the official SDK, with structured outputs.
type anthropicBackend struct {
	client anthropic.Client
}

func newAnthropic(apiKey, baseURL string) *anthropicBackend {
	// The gateway owns retries (REQ-103), so the SDK does not retry.
	opts := []option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(0)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &anthropicBackend{client: anthropic.NewClient(opts...)}
}

func (b *anthropicBackend) Call(ctx context.Context, model string, c Call) (Raw, error) {
	var schema map[string]any
	if err := json.Unmarshal(c.Schema, &schema); err != nil {
		return Raw{}, err
	}
	maxTokens := c.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(c.Prompt))},
		// DEC-014: the API enforces the answer schema.
		OutputConfig: anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: schema}},
	}
	if c.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: c.System}}
	}
	resp, err := b.client.Messages.New(ctx, params)
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return Raw{}, &StatusError{Status: apierr.StatusCode, Message: apierr.Error()}
		}
		return Raw{}, err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Raw{}, fmt.Errorf("the model declined the request (%s)", resp.StopDetails.Category)
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	u := resp.Usage
	return Raw{
		Text:      text.String(),
		TokensIn:  u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		TokensOut: u.OutputTokens,
	}, nil
}
