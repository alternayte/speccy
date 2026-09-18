package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openAICompatible calls a chat completions API: OpenAI, OpenRouter, and DeepSeek share it.
type openAICompatible struct {
	kind    string
	apiKey  string
	baseURL string
	http    *http.Client
}

var defaultBaseURLs = map[string]string{
	KindOpenAI:     "https://api.openai.com/v1",
	KindOpenRouter: "https://openrouter.ai/api/v1",
	KindDeepSeek:   "https://api.deepseek.com",
}

func newOpenAICompatible(kind, apiKey, baseURL string) *openAICompatible {
	if baseURL == "" {
		baseURL = defaultBaseURLs[kind]
	}
	return &openAICompatible{kind: kind, apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

func (b *openAICompatible) Call(ctx context.Context, model string, c Call) (Raw, error) {
	prompt := c.Prompt
	var format any
	if b.kind == KindDeepSeek {
		// DeepSeek has JSON mode but no schema mode: ask for the schema in the prompt.
		format = map[string]any{"type": "json_object"}
		prompt += schemaInstruction(c.Schema)
	} else {
		format = map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "answer", "schema": json.RawMessage(c.Schema), "strict": false,
		}}
	}
	messages := []map[string]string{}
	if c.System != "" {
		messages = append(messages, map[string]string{"role": "system", "content": c.System})
	}
	messages = append(messages, map[string]string{"role": "user", "content": prompt})
	body := map[string]any{"model": model, "messages": messages, "response_format": format}
	if c.MaxTokens > 0 {
		body["max_tokens"] = c.MaxTokens
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return Raw{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return Raw{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	resp, err := b.http.Do(req)
	if err != nil {
		return Raw{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Raw{}, err
	}
	if resp.StatusCode/100 != 2 {
		return Raw{}, &StatusError{Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Raw{}, fmt.Errorf("the response does not parse: %w", err)
	}
	if len(out.Choices) == 0 {
		return Raw{}, fmt.Errorf("the response has no choices")
	}
	if r := out.Choices[0].Message.Refusal; r != "" {
		return Raw{}, fmt.Errorf("the model declined the request: %s", r)
	}
	return Raw{Text: out.Choices[0].Message.Content, TokensIn: out.Usage.PromptTokens, TokensOut: out.Usage.CompletionTokens}, nil
}

// errorMessage returns the message of an API error body, or the start of the body.
func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := string(body)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
