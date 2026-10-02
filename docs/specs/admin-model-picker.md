# Model picker in Admin

## What it does
An admin picks the model of a role from a list, not from memory. The list comes live from the backend, and a search field narrows it. For OpenRouter, the price fields fill in from the same list.

## Decisions
- The picker is a dropdown with a search field for the four API backends: OpenRouter, OpenAI, Anthropic and DeepSeek. — Each has a models endpoint, so the list is never stale.
- Speccy calls the models endpoint with the key that the backend stores. The key stays on the server. — The browser must not hold an API key.
- The price fields fill in for OpenRouter only. — Its models endpoint returns prices. The other three return none, and a copied price table goes stale.
- The price fields stay editable for every backend. — A team can have its own rates.
- An agent CLI backend keeps the free-text model field. — A CLI has no models endpoint.
- When the list does not load, the picker shows the cause and the free-text field. — A provider outage must not stop the setup.
- A model that is assigned and is no longer in the list stays assigned, with a note. — A role must not lose its model without an act of the admin.

## Out
- No price lookup for OpenAI, Anthropic or DeepSeek.
- No check that a model can do a role's job, such as structured output or search.
- No change to budgets or to the cost estimate.
- Built after `docs/specs/fix-findings.md`. The two share no code.

## How I know it works
- A role on an OpenRouter backend shows the list of OpenRouter models. Typing "gpt" narrows it.
- Picking an OpenRouter model fills both price fields. The values equal the prices on the model's OpenRouter page, in dollars per million tokens.
- A role on an Anthropic backend shows the models of that account, and the price fields stay as they are.
- With the network off, the picker shows the error and the free-text field, and a typed model saves.
- A role on an agent CLI backend shows the free-text field only.
