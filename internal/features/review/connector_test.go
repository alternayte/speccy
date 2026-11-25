package review_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// fakeConnector serves an index and its pages from a map, and records each fetch.
type fakeConnector struct {
	base, index string
	pages       map[string]string
	profiles    []string
	mu          sync.Mutex
	fetched     []string
}

func (c *fakeConnector) Name() string       { return "platform-docs" }
func (c *fakeConnector) Index() string      { return c.index }
func (c *fakeConnector) Owns(u string) bool { return review.Under(u, c.base) }
func (c *fakeConnector) Applies(p string) bool {
	return len(c.profiles) == 0 || strings.Contains(strings.Join(c.profiles, ","), p)
}
func (c *fakeConnector) Fetch(_ context.Context, u string) (string, error) {
	c.mu.Lock()
	c.fetched = append(c.fetched, u)
	c.mu.Unlock()
	if t, ok := c.pages[u]; ok {
		return t, nil
	}
	return "", fmt.Errorf("%s answered 404 Not Found", u)
}

// docs/specs/url-connectors.md: a claim that no file settles goes to the connector before any
// search. The pick reads the index, Speccy fetches only the picked pages under the base URL,
// and a page that states the claim verifies it with the page URL as the source. A page that
// states another value contradicts it as a MUST that names the page. A second review reads
// the pick from the cache, and a connector for another profile is not asked.
func TestGrounding_URLConnector(t *testing.T) {
	base := "https://docs.acme.dev/platform/"
	conn := &fakeConnector{base: base, index: base + "llms.txt", pages: map[string]string{
		base + "llms.txt": "# Platform\n\n> The platform docs.\n\n## Docs\n\n" +
			"- [Request limits](limits.md): body sizes\n- [The database](db.md): engines and regions\n" +
			"- [Billing](billing.md): invoices\n- [Auth](auth.md)\n- [Queues](queues.md)\n- [Search](search.md)\n" +
			"- [Other limits](https://elsewhere.example/limits.md)\n",
		base + "limits.md": "# Limits\n\nThe provider caps each request body at 500 kilobytes for every endpoint.\n",
		base + "db.md":     "# Database\n\nThe orders database runs on Postgres 17 in every region.\n",
	}}
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	pe.reviews.Connectors = func(context.Context) ([]review.Connector, error) { return []review.Connector{conn}, nil }
	run, fs, _ := pe.run(t, "pay")

	if f, c := groundingOf(t, pe, run, fs, "Postgres 17"); f.CheckSlug != "" || c.Label != "verified" || !strings.Contains(string(c.Sources), base+"db.md") {
		t.Errorf("the claim the page states: finding %q, label %s, sources %s; want verified by db.md", f.CheckSlug, c.Label, c.Sources)
	}
	f, _ := groundingOf(t, pe, run, fs, "999 kilobytes")
	if f.CheckSlug != review.GroundingContradicted || f.Level != "MUST" || !strings.Contains(f.Message, base+"limits.md") || !strings.Contains(f.Message, "500 kilobytes") {
		t.Errorf("the claim the page contradicts: %s at %s: %q; want a MUST that names the page and quotes it", f.CheckSlug, f.Level, f.Message)
	}
	for _, p := range pe.fake.verify {
		if strings.Contains(p, "Postgres 17") || strings.Contains(p, "999 kilobytes") {
			t.Error("a claim that a connector page settles went to the search")
		}
	}
	for _, u := range conn.fetched {
		if !strings.HasPrefix(u, base) {
			t.Errorf("Speccy fetched %s, outside the base URL", u)
		}
		if strings.HasSuffix(u, "billing.md") || strings.HasSuffix(u, "auth.md") {
			t.Errorf("Speccy fetched %s, which the pick did not choose", u)
		}
	}

	picks := pe.fake.count(review.PromptConnectorPick)
	pe.run(t, "pay")
	if n := pe.fake.count(review.PromptConnectorPick); n != picks || picks == 0 {
		t.Errorf("%d pick calls, then %d after a review of the same text", picks, n)
	}

	other := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	conn.profiles = []string{"prd"}
	other.reviews.Connectors = func(context.Context) ([]review.Connector, error) { return []review.Connector{conn}, nil }
	other.run(t, "pay")
	if n := other.fake.count(review.PromptConnectorPick); n != 0 {
		t.Errorf("a connector for the prd profile made %d pick calls on an SDD", n)
	}
}
