# Configuration

## Local mode

`speccy` serves the bundles in a folder on your machine. It has no sign-in.

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | the current folder | The folder with the bundles. |
| `--addr` | `127.0.0.1:7878` | The address. It must be a loopback address. |
| `--no-open` | off | Do not open the browser. |

Speccy keeps its state in `<dir>/.speccy/state/`. Do not commit that folder.

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

Speccy applies its database migrations at start. Keep the master key safe: without it, Speccy cannot read the stored API keys.

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

### API tokens

A person makes an API token under **Account**. The CLI, CI, and MCP clients send it as `Authorization: Bearer <token>`. A token has the role of the person, and it stops when the person loses the role.

### Workspace settings

An admin sets these under **Admin → Workspace settings**.

| Setting | Default | Range |
|---|---|---|
| Model calls at a time, per review | 4 | 1 to 16 |
| Largest file | 10 MB | 1 to 50 MB |
| Largest bundle | 50 MB | the file limit to 500 MB |
| Invite links expire after | 7 days | 1 to 90 days |

### Rate limits

Speccy limits sign-in, the invite and reset links, password changes, and two-factor codes for each client address. The counts are in the database, so every instance shares them. A refused request gets `429`.

## Development

`just dev-hosted` starts Postgres with Docker, the server in hosted mode, and the Vite dev server. `just invite admin` prints the first invite link for it. The master key and the URLs of these recipes are for development only.
