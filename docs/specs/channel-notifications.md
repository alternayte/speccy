# Channel notifications

## What it does
An admin pastes a Slack or Teams incoming webhook URL in Admin → Workspace settings. Speccy then posts one message to that channel for each new inbox item, each time a bundle reaches Build Ready, and when a budget reaches 80%. Each message names the person it waits for and links to the item. Speccy sends no mail.

## Decisions
- One webhook per workspace, of the kind Slack or Teams. The admin picks the kind. Reason: one paste reaches the team, with no mail server (DEC-016 holds).
- This replaces the SDD §2 line "Webhook notifications (Slack, Teams). 0.1.0 has an in-app inbox only." Reason: on a team of 30, approvals that only reach the inbox stall.
- Messages: review requested (names the reviewers), waiver requested (names the approvers), blocking thread opened (names the author), Build Ready reached (names the authors), budget at 80% (names the queue). Reason: each is a step where someone must act or can move on.
- A message holds the bundle title, the event in one sentence, the names, and one link to the item. It holds no doc text and no finding text. Reason: the channel may be wider than the bundle's visibility.
- A Private bundle posts nothing. Reason: the channel cannot see who may read the bundle.
- Slack gets `{"text": ...}` with a link in Slack's `<url|title>` form. Teams gets the payload that the current Teams Workflows webhook docs require, checked when it is built. Reason: each kind's own format.
- `SPECCY_NOTIFY_WEBHOOK_URL` and `SPECCY_NOTIFY_WEBHOOK_KIND` set it instead. When set, they win and lock the UI field. Reason: the owner rule for configuration.
- The URL is a secret. It is stored encrypted with `SPECCY_MASTER_KEY`, and the UI shows its last 4 characters. Reason: anyone with the URL can post to the channel.
- Each message is a River job in the `interactive` queue. It retries 3 times, then logs the failure. Reason: a slow or failed webhook never blocks the action that caused it.
- **Send a test message** posts one message and shows the HTTP status. Reason: the admin knows at once whether the URL works.
- Local mode has no webhook. Reason: one person works there, and the inbox is enough.

## Out
- Email.
- Direct messages, and @mentions through a Slack user mapping.
- More than one channel, or routing by profile or repo.
- Notifications for PR reviews. GitHub notifies those.

## How I know it works
- Paste a Slack webhook and select **Send a test message**. The channel shows the message, and the UI shows 200.
- Request a review on an Internal bundle. The channel shows one message with the bundle title, the reviewers' names and a link that opens the bundle.
- Request a review on a Private bundle. The channel shows nothing.
- Point the URL at a server that returns 500. The review request still succeeds. River UI shows the job retried 3 times, then discarded.
- Set `SPECCY_NOTIFY_WEBHOOK_URL`. The field in Admin is locked.
