# URL connectors

Issue: #169.

## What it does
An admin adds a URL connector: a name and the address of a doc index, such as an `llms.txt`, on the web or in a GitHub repo. A review reads it the way an agent does. The grounding stage reads the index, picks the pages that fit the claims that the bundle's files leave open, and checks the claims against those pages with the word-for-word quote rule. A connector also reads the external links on its own base URL. The admin section that holds MCP connections and URL connectors is called Connectors. No MCP server is needed.

## Decisions
- The grounding stage checks the bundle's files first, then the connectors, then web search or MCP search. A claim that a connector settles goes no further — the closest evidence comes first, as in grounding-bundle-sources.md.
- A connector checks internal and external claims. The rule that an internal claim never goes to web search stays — an internal platform's own docs are the reason for a connector.
- For each batch of claims that no file settles, one model call reads the index and picks up to 5 links. Speccy fetches only those pages — an llms.txt is an index, and a large site's llms-full.txt runs from 2 to 56 MB, too large to read whole.
- The pick takes any link of the index, the "Optional" section too, one level deep. Speccy drops each link outside the connector's base URL before the pick — the model picks pages, never a host, so a doc cannot steer a fetch.
- A claim counts as verified or contradicted by a page only with a word-for-word quote from it. Its source is the page URL. A pick that misses the right page leaves the claim unverified, never wrongly verified — the rule of the file pass.
- The base URL is the folder of the index unless the admin sets it — Context7 limits a crawl the same way.
- A web connector takes one optional header: a name and a value, such as `Authorization: Bearer …` or `X-Api-Key: …`. Speccy seals the value with the master key, shows its last 4 characters, and sends it only to the base URL's host — gateways for internal docs want a named header.
- A connector address can be a GitHub URL of a file or a folder at a branch, such as `https://github.com/acme/platform/blob/gh-pages/llms.txt`. Speccy reads it with its GitHub credential: the App or the token in hosted mode, `gh` in local mode. A link of the index that starts with the site's URL maps to the file at the same path in that branch — a private GitHub Pages site accepts no token, and the repo behind it does.
- A GitHub connector takes no header — the GitHub credential reads it.
- A connector has an optional list of profiles. With no list it applies to every doc. With a list it applies only to docs of those profiles — one pick call per connector per batch is the cost, and a list cuts it.
- A page over 200,000 characters is left out, and the run report names it — the asset bound of the grounding stage.
- Speccy fetches the index and the pages it needs at each review. The model results are cached by the claim and the page content. A failed fetch keeps the last good text of that page and adds a run note — a GET costs nothing next to a model call, and a failed fetch must not hide evidence.
- The source policy does not apply to a connector page — the admin chose it, as an author chooses a bundle file.
- A connector reads an external link whose URL is under its base URL, with a GET and its header. The `coherence.external` check reads that text as it reads an MCP fetch. An MCP connection for the same host comes second — one place reads a target, and the plain fetch costs no MCP session.
- The reason of an unchecked link names "Admin → Connectors".
- One table, `url_connector`, holds the connectors. It shares no field with `mcp_connection` but the name. The conformance suite covers it on SQLite and Postgres.
- The admin section "MCP connections" becomes "Connectors". It lists both kinds, each with its kind on the row. "Add a connector" asks for the kind first. An admin configures connectors in the web app only, as MCP connections today.
- A claim that a connector page settles shows the page URL as its source in the claim list and the report.

## Out
- Links deeper than one level from the index.
- OAuth, cookie or browser sign-in for a connector.
- A connector in the source policy's `allow` or `forbid` lists.
- Connectors in the TUI, the CLI or the MCP tools.
- Embeddings or a search index over connector pages.

## How I know it works
- Add a connector for a public llms.txt whose pages state "The ledger API caps a refund at 10,000 EUR". A bundle whose doc claims that cap gets no grounding finding, and the claim list shows the page URL as its source. The run made no web search for that claim.
- The same doc, with the claim changed to 20,000 EUR, gets `grounding.contradicted-claim` that quotes the page.
- A second review with no change makes no fetch call to the model and reads every label from the cache.
- A connector with the profile list `sdd`: a PRD review makes no pick call for it.
- A connector with a header: the server's access log shows the header on requests to its host, and on no other host.
- A connector at `https://github.com/<org>/<repo>/blob/gh-pages/llms.txt` on a private repo that the App reads: the pages resolve to files of that branch, and a claim they state is verified.
- An index link to another host does not get fetched, and the run report does not name it as read.
- A page of 300,000 characters is left out, and the run report names it.
- An external link `https://docs.acme.dev/ledger/refunds` with a connector on `https://docs.acme.dev/ledger/`: the link reads checked, and no MCP connection is called.
- Admin shows one section, Connectors, with the MCP connections and the URL connectors in one list.
- The pick prompt and the connector check, in turns with the v0.33 prompts on the same bundle and a real model, give no MUST that v0.33 does not give.
