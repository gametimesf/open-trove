# Slack integration design

Status: in progress. The notification domain ships first; the Slack adapter
follows. Both are off unless configured.

## Goals

- Tell people in Slack when someone comments on their artifact, replies in a
  thread they're part of, or resolves or reopens their thread.
- Show a useful preview when a Trove link is pasted in Slack.
- Work for deployments that are only reachable on a private network.

## Non-goals (for now)

- Rendered image previews. They need server-side rendering of untrusted HTML,
  which must run isolated with no network access. Metadata previews come first.
- Replying or resolving from Slack, and per-user notification settings.
- Channel notifications. Messages go to individuals only.

## Shape

```
comments.Service ──(activity, after a successful write)──▶ notify.Dispatcher
                                                            │ recipients, retry
                                                            ▼
                                                      notify.Notifier ──▶ Slack DM
Slack link_shared (Socket Mode) ──▶ unfurl handler ──▶ artifact metadata ──▶ chat.unfurl
```

- `comments` reports activity through an optional `Observer` after the write is
  stored. It knows nothing about Slack.
- `notify` decides who to tell and delivers in the background. It depends on a
  `Notifier` interface, so Slack is one adapter among possible others (email).
- The Slack adapter owns Slack specifics: Socket Mode, user lookup by email,
  message and unfurl formatting.

## Who gets notified

| Activity | Recipients |
|---|---|
| New thread | Artifact owner |
| Reply | Artifact owner, plus everyone with a live comment in the thread |
| Resolved / reopened | Thread author |

The person who acted is never notified, and each person gets one message per
activity. Edits and deletions are not notified.

Recipients are identified by email. Until verified identity is enforced (see
`docs/authentication.md`), owner and author emails are asserted, so a person
could be notified about an artifact someone else uploaded under their email.
Operators should enable notifications together with, or after, enforcement.

## Delivery

- Asynchronous: comment writes enqueue and return. A bounded queue (1,000)
  drops and logs when full rather than slowing writes.
- Up to three attempts per recipient with backoff; a recipient that can never
  be reached (no Slack account for the email) is not retried.
- No persistence: activity queued at shutdown is lost. Acceptable for
  notifications; revisit if delivery guarantees are needed.

## Slack connection

- Socket Mode only. Trove opens an outbound WebSocket to Slack, so Slack never
  needs to reach Trove and no inbound endpoint, signing secret, or public
  ingress is required.
- Tokens come from the environment, never YAML: `SLACK_BOT_TOKEN` (bot user
  OAuth token) and `SLACK_APP_TOKEN` (app-level token with
  `connections:write`).
- `docs/slack-app-manifest.yaml` (added with the adapter) is the auditable scope set.

## Link previews

- Handle `link_shared` for links whose host matches `base_url`, pointing at an
  artifact (`/{slug}`, `/{slug}/raw`, site pages).
- Preview: title, type, uploader, open comment count. No file content.
- Skip previews in channels shared with other organizations (Slack Connect). If
  the channel can't be checked, skip — fail closed.

## Data handling

Comment excerpts and artifact titles are internal business data. They go only
to individual members of the workspace (DMs) and to previews in channels that
aren't shared externally.

## Observability

Log lines per notification (`notification sent|failed|dropped|skipped`, with
kind, slug and reason) and per unfurl decision, without tokens or message
bodies.
