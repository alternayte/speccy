# GitHub App

## What it does
A new hosted Speccy opens /setup with a setup token. The operator makes the first admin, then connects GitHub. Speccy creates a private GitHub App from a manifest on an org or a personal account, and the admin installs the App. Sources read through the App, and a push webhook starts a sync at once. Members of the owning org sign in with GitHub. A doc change goes to GitHub as a pull request that the person who published it authors. The App bot authors no doc change.

## Decisions
- `SPECCY_SETUP_TOKEN` opens /setup. Step 1 makes the first admin with email and password. Step 2 connects GitHub with an App or a token, and the operator can skip it — self-hosting needs no shell in the pod.
- The setup token stops working when an admin exists. With no `SPECCY_SETUP_TOKEN`, /setup is off. `speccy admin invite` stays for recovery — the token gives no access after the first admin.
- The workspace holds one GitHub connection: an App or a fine-grained token, never both. Setup and Admin → GitHub offer the App first — each call has one credential, and the token covers an org that forbids new Apps, and GHE.com.
- The App is private. The admin types an org name. An empty field makes the App on the admin's personal account — only the owning account can install a private App, so no stranger sends events.
- Setup takes the GitHub host, github.com by default. A GHES host uses its own manifest and API addresses — a source already holds an API URL.
- The manifest asks for contents write, pull requests write, issues write, metadata read, email addresses read, and members read on an org. It subscribes to push and pull_request. The hook URL is `SPECCY_BASE_URL/api/github/webhook`. `setup_url` comes back to Admin → GitHub with `setup_on_update` — the least that sources, publish, sign-in and the breakdown's issues, the sign-in's verified email, and the membership check need.
- The store holds the App ID, slug, owning account and client ID. It holds the private key, client secret and webhook secret encrypted with `SPECCY_MASTER_KEY` — the same rule as the token today.
- `SPECCY_GITHUB_APP_ID`, `_PRIVATE_KEY`, `_CLIENT_ID`, `_CLIENT_SECRET` and `_WEBHOOK_SECRET` supply the App instead, with `_API_URL` for GHES. When set, they win and lock the App fields in the UI — a GitOps install needs no click.
- Speccy records the installation from the `setup_url` return and from installation webhooks. A private App on one account has one installation — no list of accounts to manage.
- An installation token lives in memory and renews before its one hour ends. The store never holds it — a stored token is one more secret to leak.
- A source URL in a repo that the installation does not cover fails with "The Speccy App is not installed on <repo>" and the install link — the person fixes it in one step.
- The webhook checks `X-Hub-Signature-256` and refuses a bad one with 401. A push to a source's branch syncs that source. A closed or merged pull_request updates the draft state — GitHub does not retry, so polling stays.
- Polling stays every 5 minutes for every connection — a host that GitHub cannot reach still syncs.
- The GitHub sign-in button uses the App's user OAuth. `SPECCY_GITHUB_OAUTH_*` goes away — one source for GitHub sign-in. Email, password and OIDC cover a token workspace.
- A member of the owning org gets an account with the member role at the first GitHub sign-in, with no invite. An App on a personal account admits only that user. Speccy checks the membership with the installation token at each GitHub sign-in — GitHub already knows who belongs.
- A GitHub sign-in never links to an account by email. An invited person links GitHub under Account, or at the first Publish — Speccy never verifies an invite email, so an email match would let one person take another's account.
- Publish, publish of mappings, and their commits use the person's GitHub user token. Speccy stores each person's refresh token encrypted and renews it — the person is the author on GitHub, so branch protection holds, and the author cannot approve their own change.
- Publish by a person with no GitHub link starts the link OAuth, then publishes. A refresh that fails asks the person to connect again — the bot never authors a doc change.
- Reads, sync and the membership check use the installation token — they act for the workspace, not for a person.
- In a token workspace, the token's owner authors every change, polling is the only sync, and GitHub sign-in is off — as today.
- Disconnect in Admin → GitHub deletes the stored App credentials and the user tokens. Sources stay and say there is no GitHub connection. The screen links to the App's settings page on GitHub for deletion — the API cannot delete an App.
- Local mode does not change: the `gh` login, then a pasted token — local mode has no public URL for webhooks.

## Out
- PR review by the server. The Action keeps the review comments and check runs.
- Pending reviews in hosted mode. They stay refused.
- A public App, or more than one installation.
- A webhook relay, a delivery list, and redelivery.
- A map from GitHub teams to roles.
- The manifest flow on GHE.com. The token covers it.

## How I know it works
- Start hosted with `SPECCY_SETUP_TOKEN`. A wrong token at /setup fails. The right token shows the admin form, then the GitHub step. After the admin exists, /setup returns 404.
- Create the App on a test org. GitHub shows its confirm page. Admin → GitHub then shows the App slug, the org, and "Not installed". Install it, and the screen shows the installation.
- Add a source URL in a covered repo. It syncs. A repo outside the installation shows the install message.
- Push to a source branch. The source syncs within seconds, not at the next poll.
- Send a webhook with a bad signature. Speccy answers 401.
- An org member with no invite signs in with GitHub and gets a member account. A non-member is refused.
- An email account clicks Publish. GitHub asks to authorize. The pull request and its commit show that person as author.
- Revoke the authorization in GitHub settings. The next Publish asks to connect again.
- Set the `SPECCY_GITHUB_APP_*` variables. The App fields in Admin → GitHub are locked.
- Disconnect. Sources say there is no GitHub connection. Paste a token, and they sync again.
