package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
)

// ModelsTimeout is the limit for one read of a backend's models endpoint.
const ModelsTimeout = 20 * time.Second

// Info is one model that a backend's models endpoint lists. Only OpenRouter gives prices, in
// dollars per million tokens.
type Info struct {
	ID       string
	Name     string
	PriceIn  *float64
	PriceOut *float64
}

// lister is a backend with a models endpoint. An agent CLI has none.
type lister interface {
	Models(ctx context.Context) ([]Info, error)
}

// Models reads the models of a backend live from its models endpoint, with the key that the
// backend stores. The key stays on the server.
func (g *Gateway) Models(ctx context.Context, row pgdb.ModelBackend) ([]Info, error) {
	build := g.build
	if build == nil {
		build = g.backendFor
	}
	be, err := build(row)
	if err != nil {
		return nil, err
	}
	l, ok := be.(lister)
	if !ok {
		return nil, kernel.Invalid("no_model_list", "The %s backend has no model list. Type the name of the model.", row.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
	defer cancel()
	out, err := l.Models(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, kernel.Invalid("models_unavailable", "The %s backend did not give its model list within %s. Type the name of the model.", row.Name, ModelsTimeout)
		}
		return nil, kernel.Invalid("models_unavailable", "The %s backend did not give its model list: %s. Type the name of the model.", row.Name, strings.TrimRight(err.Error(), "."))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Models reads GET /models, which OpenAI, OpenRouter and DeepSeek share. OpenRouter adds the
// price of one token as a decimal string.
func (b *openAICompatible) Models(ctx context.Context) ([]Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &StatusError{Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New("the response does not parse")
	}
	out := make([]Info, 0, len(list.Data))
	for _, m := range list.Data {
		info := Info{ID: m.ID, Name: m.Name}
		if b.kind == KindOpenRouter {
			info.PriceIn, info.PriceOut = perMillion(m.Pricing.Prompt), perMillion(m.Pricing.Completion)
		}
		out = append(out, info)
	}
	return out, nil
}

// perMillion turns the price of one token, as a decimal string, into dollars per million
// tokens. It returns nil for a string that is no price, and for a negative one: OpenRouter
// writes -1 for a model whose price varies.
func perMillion(perToken string) *float64 {
	v, err := strconv.ParseFloat(perToken, 64)
	if err != nil || v < 0 {
		return nil
	}
	p := math.Round(v*1e6*1e6) / 1e6
	return &p
}

// Models reads the models that the account of the key can use.
func (b *anthropicBackend) Models(ctx context.Context) ([]Info, error) {
	var out []Info
	it := b.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{Limit: anthropic.Int(1000)})
	for it.Next() {
		m := it.Current()
		out = append(out, Info{ID: m.ID, Name: m.DisplayName})
	}
	if err := it.Err(); err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return nil, &StatusError{Status: apierr.StatusCode, Message: errorMessage([]byte(apierr.RawJSON()))}
		}
		return nil, err
	}
	return out, nil
}
