# Configuration

## Local mode

`speccy` serves the bundles in a folder on your machine. It has no sign-in.

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | the current folder | The folder with the bundles. |
| `--addr` | `127.0.0.1:7878` | The address. It must be a loopback address. |
| `--no-open` | off | Do not open the browser. |

Speccy keeps its state in `<dir>/.speccy/state/`. Do not commit that folder.

Speccy cannot read a state from a version before 0.15.0, and it stops at start with a message. Move the folder aside, then start Speccy again. Speccy makes a new state and scans the docs again. The docs and their sidecars stay as they are. The reviews and threads of the old state stay in the folder you moved.

## Commands

| Command | Does |
|---|---|
| `speccy review <path…>` | Reviews the bundles in the paths. A folder names every bundle in it; a file names its bundle. |
| `speccy init` | Writes `.speccy.yaml`, adds `.speccy/state/` to `.gitignore`, and offers to give loose markdown files a type. |
| `speccy add <url>` | Makes a GitHub source from a source URL: `owner/name`, a repo URL, or the URL of a folder or a doc. Local mode takes the token from `gh auth token`; with no `gh` login, paste a fine-grained token in Admin → GitHub. Speccy never runs `git`. |
| `speccy init --github` | Adopts a repo whose docs Speccy did not write: it guesses a profile for each markdown file, writes the path mappings, lints them all, puts every failing check in adoption mode, and writes the Action's workflow. It changes no doc. |
| `speccy tui` | The terminal UI. It uses `$VISUAL` or `$EDITOR` to open files. |
| `speccy mcp` | The MCP server over stdio. |
| `speccy profile validate <file>` | Checks a profile file against the schema and prints each error with its path. |
| `speccy export <path> --format zip\|html` | Writes the bundle as a `.zip`, or as a single HTML report with the verdict and the findings. |

`speccy review`, `tui`, `mcp`, and `export` work on the folder with `.speccy.yaml`, from the current folder up, or else on the current folder. They use `.speccy/state/` when it exists, so they share models, reviews, and threads with the app. With no `.speccy/state/`, `speccy review` uses a temporary store and leaves nothing behind.

Flags of `speccy review`:

| Flag | Default | Meaning |
|---|---|---|
| `--format` | `text` | `text`, `json`, or `md`. |
| `--summary` | off | One table for all bundles, with totals. |
| `--stages` | all, or lint when no model is assigned | A comma-separated list of `lint`, `rubric`, `grounding`, `divergence`, and `coherence`. Lint always runs. |
| `--enforcement` | `enforcement` in `.speccy.yaml`, else `advisory` | With `blocking`, a Not Build Ready verdict exits 1. |
| `--server` | none | Send the files to a Speccy server, which reviews them with its models and its linked docs. The server changes no bundle. It keeps the files and the result for 90 days, and the output links to the report. The API token is in `SPECCY_TOKEN`. |

### The GitHub Action

`action.yml` at the root of this repo is the Action. It downloads the release binary and runs `speccy action` in the folder of `.speccy.yaml`.

| Input | Default | Meaning |
|---|---|---|
| `config` | `.speccy.yaml` | The repo configuration. Speccy reviews the bundles of its folder. |
| `enforcement` | from `.speccy.yaml`, else `advisory` | `advisory` never fails the job on a verdict. `blocking` fails it on Not Build Ready. |
| `models` | none | One `role=backend:model` per line; `all` sets every role. With none, only lint runs. |
| `anthropic-api-key`, `openai-api-key`, `openrouter-api-key`, `deepseek-api-key` | none | The key of each backend that `models` uses. |
| `server`, `token` | none | Connected mode: the server and a personal API token. |
| `version` | `latest` | The Speccy release. |
| `github-token` | `${{ github.token }}` | Posts the comments and the checks. |

`speccy action` finds the bundles that the pull request changes. It posts MUST findings, and findings with a certain fix, as inline comments on changed lines: at most `pr.inline_limit` per bundle (default 15). The rest go into the summary comment. Suggestions are only for a trace ID, `must`, `should`, or `may` in capitals in a requirement, and a broken link when exactly one file in the bundle has its name. A model never writes a suggestion.

Outside GitHub, the same environment works for `speccy review`: `SPECCY_MODELS` sets the models, `SPECCY_<BACKEND>_API_KEY` their keys, and `SPECCY_STATE_DIR` a store that stays between runs.

### MCP

For a coding agent on your machine, add Speccy as an MCP server that runs `speccy mcp` in the repo folder. For example, in Claude Code:

```sh
claude mcp add speccy -- speccy mcp
```

The tools are `list_bundles`, `get_bundle`, `review_bundle`, `review_content`, `get_verdict`, `get_findings`, `get_tour`, `get_traceability`, `list_threads`, and `post_message`. `review_content` reviews text that is not saved, so an agent can check a doc before it writes the file.

In hosted mode, the same tools are at `/mcp` over streamable HTTP. Send a personal API token as `Authorization: Bearer <token>`. A tool can do what the token's owner can do in the app, and nothing more.

## Hosted mode

`speccy serve --hosted` serves a team. It needs Postgres and reads its settings from the environment.

| Variable | Required | Meaning |
|---|---|---|
| `SPECCY_DATABASE_URL` | Yes | The Postgres URL. |
| `SPECCY_MASTER_KEY` | Yes | 32 random bytes in base64. Speccy encrypts API keys and signs guest cookies with it. Make one with `openssl rand -base64 32`. |
| `SPECCY_BASE_URL` | Yes | The public URL, such as `https://speccy.example.com`. Links and cookies use it. |
| `SPECCY_LISTEN` | No | The address to listen on. Default `:8080`. |
| `SPECCY_LOG_LEVEL` | No | `debug`, `info`, `warn`, or `error`. Default `info`. Logs are JSON. |
| `SPECCY_OIDC_ISSUER`, `SPECCY_OIDC_CLIENT_ID`, `SPECCY_OIDC_CLIENT_SECRET` | No | Single sign-on with an OpenID Connect issuer, such as Entra ID. Set all three. |
| `SPECCY_GITHUB_OAUTH_CLIENT_ID`, `SPECCY_GITHUB_OAUTH_CLIENT_SECRET` | No | Sign-in with GitHub. Set both. |

Speccy applies its database migrations at start. It cannot read a database from a version before 0.15.0, and it stops at start with a message. Point `SPECCY_DATABASE_URL` at a new, empty database. Keep the master key safe: without it, Speccy cannot read the stored API keys.

Serve Speccy over HTTPS. With an `http://` base URL, the cookies lose the Secure flag. Use that for development only.

### The first admin

Run this with the same environment as the server:

```sh
speccy admin invite --role admin
```

It prints a link. Open it, and make your account. The link works once and expires after 7 days.

### Accounts

Speccy has no open sign-up and sends no mail. Every account comes from an invite link.

- An admin makes invite links under **Admin → People**, or with `speccy admin invite --role member`.
- An admin makes a password reset link for a person under **Admin → People**, or with `speccy admin reset-link <email>`. The link works once, for 24 hours. It signs the person out everywhere.
- The email is the sign-in name. Speccy does not check it by mail.

Pass each link on yourself, for example in a direct message.

### Single sign-on and GitHub

A provider signs in a person who has an account already. The person signs in with a password first, then links the provider under **Account**. Speccy does not link a provider to an account by email.

Register these callback URLs at the provider:

| Provider | Callback URL |
|---|---|
| OIDC | `<SPECCY_BASE_URL>/api/auth/oauth/oidc/callback` |
| GitHub | `<SPECCY_BASE_URL>/api/auth/oauth/github/callback` |

### Roles

| Role | Can |
|---|---|
| Admin | Everything, including models, people, and settings. |
| Member | Create bundles, and edit the bundles they author. Read internal bundles and run reviews on them. |
| Guest | Read one bundle through its share link. A guest cannot edit or run a review. |

### Who can see a bundle

The author sets this under **Share** on the bundle page.

| Visibility | Who can see the bundle |
|---|---|
| Private | Its authors, its named reviewers, and admins. |
| Internal | Every member. This is the default. |
| Link | Every member, and anyone with the share link as a guest. |

A new share link replaces the old one. Revoking the link ends every guest's access at once.

### Review, waivers, and approval

- An author asks for a review and names the reviewers on the bundle page. The bundle moves from Draft to In review.
- A reviewer approves the current version. Approval needs a current Build Ready verdict. An author cannot approve their own bundle. A profile sets how many approvals it needs (`approvals.required`).
- Any change to the doc after approval revokes the approvals and moves the bundle back to In review.
- A member asks for a waiver of one finding, with a reason of at least 20 characters. The profile's waiver policy says who approves it: `any_member`, `non_author`, `n_approvals: N`, `maintainer`, or `forbidden`. An approved waiver goes into the doc's sidecar, `.speccy/decisions/<doc path>.yaml`. The doc text does not change. The waiver ends when its section changes.
- A thread marked blocking keeps the doc Not Build Ready until someone resolves it.

### Profiles

A profile sets the template, the checks, the limits, and the policies of one doc type. In hosted mode, maintainers of a profile and admins edit it under **Profiles**; each save is a new version, and earlier reviews keep theirs. An admin names the maintainers on the profile page. In local mode, **Profiles** saves to `.speccy/profiles/<key>.yaml` and its template file.

Any member suggests a change to a profile under **Profiles → Suggestions**.

#### The source policy

`grounding.sources` says which sources the grounding stage accepts. Speccy applies it after
the model returns a URL, so no rule depends on a model obeying it.

```yaml
grounding:
  sources:
    allow: [docs.example.com, "*.example.org"]   # only these hosts, when the list exists
    forbid: [answers.example.net]                # Speccy never requests these
    domains:
      - { pattern: docs.example.com, tier: primary }
    freshness: { primary: 180, secondary: 30 }   # days; 0 is no limit
    require_primary: [security]                  # these claim classes need a primary source
    classes:
      - { pattern: "Security/**", class: security }
    unclassified: warn                           # allow, warn, or require-classification
```

A claim takes the class of the section it is anchored in, by its heading path. A `*` segment
matches one heading, and a trailing `**` matches the rest of the path. The most specific
pattern wins. Two equally specific patterns that both match one section are a fault, and the
profile does not load. No model assigns or changes a class.

A source whose every hop passes the policy keeps its tier. A source the policy refuses is
dropped, and the claim says why. A claim whose sources are all dropped is `unverified`.

#### The requirement grammar

`lint.requirement-grammar` parses each trace ID definition into a trigger and a response, in
the EARS shapes. It is off until a profile names a level:

```yaml
lint:
  overrides:
    lint.requirement-grammar: { level: SHOULD }
```

A definition parses as `The <actor> MUST <response>`, or with a leading `When`, `While`,
`Where` or `If` and a comma before the actor. A definition that does not parse keeps its own
level; only the grammar finding appears.

### API tokens

A person makes an API token under **Account**. The CLI, CI, and MCP clients send it as `Authorization: Bearer <token>`. A token has the role of the person, and it stops when the person loses the role.

### GitHub

An admin sets one GitHub token for the workspace in **Admin → GitHub**. Use a fine-grained personal access token with read and write access to **Contents** and **Pull requests** on the repos Speccy reads. For GitHub Enterprise Server, set the API address, such as `https://github.example.com/api/v3`.

A **GitHub source** is a repo, a branch, and a folder. Speccy reads the bundles in the folder with the same rules as local mode, including the `.speccy.yaml` at the root of the repo. It reads the branch again every 5 minutes, and when an admin selects **Sync**.

An edit in Speccy to a bundle from GitHub stays unpublished. The author selects **Unpublished → Open a pull request**. Speccy puts the changed files on a new branch, `speccy/<bundle>-v<version>-<time>`, and opens a pull request into the source branch. It never changes the source branch itself. When the pull request merges, the next sync marks the bundle as published. **Discard the changes** takes the text from GitHub again.

The admin who adds a source becomes the author of its bundles.

### Workspace settings

An admin sets these under **Admin → Workspace settings**.

| Setting | Default | Range |
|---|---|---|
| Model calls at a time, per review | 4 | 1 to 16 |
| Largest file | 10 MB | 1 to 50 MB |
| Largest bundle | 50 MB | the file limit to 500 MB |
| Invite links expire after | 7 days | 1 to 90 days |
| Read the metadata of each grounding source | On | On or off |

The source resolver reads a source's redirect chain, its status and its dates. It reads no
response body, so no retrieved text reaches a model through it. With it off, a profile rule
that needs a redirect chain or a retrieval date drops the source instead of passing it.

### Rate limits

Speccy limits sign-in, the invite and reset links, password changes, and two-factor codes for each client address. The counts are in the database, so every instance shares them. A refused request gets `429`.

## Development

`just dev-hosted` starts Postgres with Docker, the server in hosted mode, and the Vite dev server. `just invite admin` prints the first invite link for it. The master key and the URLs of these recipes are for development only.
